package httpapi_test

// Conformance runner: replays spec/conformance/cases/*.json against the HTTP
// handler built by httpapi.New, the same constructor cmd/tunna uses.
//
// Scope for now: cases that need no fixtures and no credentials. Cases that
// need a signer (package sig) or provisioned keys and buckets are skipped
// with a reason, and will run once those exist. When the store adapters
// land, this file moves to where it may import them (ADR-0005, rule 7).

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/tunnaio/tunna/internal/httpapi"
)

const specDir = "../../spec"

// --- spec file shapes (subset of the schemas, enough to run) ---

type caseFile struct {
	Spec  string            `json:"spec"`
	Cases []conformanceCase `json:"cases"`
}

type conformanceCase struct {
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Fixtures    *[]string `json:"fixtures"` // nil means "all"
	Steps       []step    `json:"steps"`
}

type step struct {
	Name    string            `json:"name"`
	Request request           `json:"request"`
	Expect  expect            `json:"expect"`
	Capture map[string]string `json:"capture"`
}

type request struct {
	Method         string            `json:"method"`
	Path           []string          `json:"path"`
	Query          [][2]string       `json:"query"`
	Headers        map[string]string `json:"headers"`
	Auth           json.RawMessage   `json:"auth"`
	Body           *body             `json:"body"`
	ExpectContinue bool              `json:"expect_continue"`
}

type body struct {
	JSON  json.RawMessage `json:"json"`
	Text  *string         `json:"text"`
	Hex   *string         `json:"hex"`
	Bytes *genBytes       `json:"bytes"`
}

type genBytes struct {
	Seed   int64 `json:"seed"`
	Length int64 `json:"length"`
}

type expect struct {
	Status     *int                       `json:"status"`
	Error      *string                    `json:"error"`
	Headers    map[string]json.RawMessage `json:"headers"`
	JSON       json.RawMessage            `json:"json"`
	JSONAbsent []string                   `json:"json_absent"`
	BodySHA256 *string                    `json:"body_sha256"`
	BodyLength *int64                     `json:"body_length"`
	BodyEmpty  *bool                      `json:"body_empty"`
	BodyBytes  *genBytes                  `json:"body_bytes"`
}

type errorTable struct {
	Errors []struct {
		Code   string `json:"code"`
		Status int    `json:"status"`
	} `json:"errors"`
}

// --- the runner ---

func TestConformance(t *testing.T) {
	version := strings.TrimSpace(mustRead(t, filepath.Join(specDir, "VERSION")))
	statusOf := loadErrorTable(t)

	files, err := filepath.Glob(filepath.Join(specDir, "conformance", "cases", "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no case files under %s: %v", specDir, err)
	}

	srv := httptest.NewServer(httpapi.New("test"))
	defer srv.Close()

	for _, file := range files {
		var cf caseFile
		if err := json.Unmarshal([]byte(mustRead(t, file)), &cf); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		if cf.Spec != version {
			t.Errorf("%s declares spec %q, spec/VERSION is %q", filepath.Base(file), cf.Spec, version)
		}
		for _, c := range cf.Cases {
			c := c
			t.Run(c.Name, func(t *testing.T) {
				if reason := unsupported(c); reason != "" {
					t.Skip(reason)
				}
				runCase(t, srv, c, statusOf)
			})
		}
	}
}

// unsupported returns a reason to skip while the harness lacks fixtures and a signer.
func unsupported(c conformanceCase) string {
	if c.Fixtures == nil || len(*c.Fixtures) > 0 {
		return "needs provisioned fixtures; no store adapter yet"
	}
	for _, s := range c.Steps {
		if string(s.Request.Auth) != `"none"` {
			return "needs a signer (package sig) and key fixtures"
		}
	}
	return ""
}

func runCase(t *testing.T, srv *httptest.Server, c conformanceCase, statusOf map[string]int) {
	captured := map[string]string{}
	for i, s := range c.Steps {
		name := s.Name
		if name == "" {
			name = fmt.Sprintf("step-%d", i+1)
		}
		sub := func(in string) string { return substitute(in, captured) }

		// URL
		var path strings.Builder
		if len(s.Request.Path) == 0 {
			path.WriteString("/")
		}
		for _, seg := range s.Request.Path {
			path.WriteString("/")
			path.WriteString(encode(sub(seg)))
		}
		var query []string
		for _, kv := range s.Request.Query {
			query = append(query, encode(sub(kv[0]))+"="+encode(sub(kv[1])))
		}
		url := srv.URL + path.String()
		if len(query) > 0 {
			url += "?" + strings.Join(query, "&")
		}

		// Body
		var reqBody io.Reader
		if b := s.Request.Body; b != nil {
			switch {
			case b.JSON != nil:
				reqBody = strings.NewReader(sub(string(b.JSON)))
			case b.Text != nil:
				reqBody = strings.NewReader(sub(*b.Text))
			case b.Hex != nil:
				raw, err := hex.DecodeString(*b.Hex)
				if err != nil {
					t.Fatalf("%s: bad hex body: %v", name, err)
				}
				reqBody = bytes.NewReader(raw)
			case b.Bytes != nil:
				reqBody = bytes.NewReader(generate(b.Bytes.Seed, b.Bytes.Length))
			}
		}

		req, err := http.NewRequest(s.Request.Method, url, reqBody)
		if err != nil {
			t.Fatalf("%s: building request: %v", name, err)
		}
		for k, v := range s.Request.Headers {
			req.Header.Set(k, sub(v))
		}
		if s.Request.Body != nil && s.Request.Body.JSON != nil && req.Header.Get("Content-Type") == "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if s.Request.ExpectContinue {
			req.Header.Set("Expect", "100-continue")
		}

		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatalf("%s: reading response: %v", name, err)
		}

		check(t, name, s.Expect, resp, respBody, statusOf)

		for key, from := range s.Capture {
			val, err := capture(from, resp, respBody)
			if err != nil {
				t.Fatalf("%s: capture %s: %v", name, key, err)
			}
			captured[key] = val
		}
	}
}

func check(t *testing.T, name string, e expect, resp *http.Response, body []byte, statusOf map[string]int) {
	wantStatus := e.Status
	if e.Error != nil {
		code := *e.Error
		st, ok := statusOf[code]
		if !ok {
			t.Fatalf("%s: expects error %q which is not in errors.json", name, code)
		}
		if wantStatus == nil {
			wantStatus = &st
		}
		var eb struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(body, &eb); err != nil {
			t.Errorf("%s: error body is not JSON: %v\n%s", name, err, body)
		} else if eb.Error.Code != code {
			t.Errorf("%s: error code = %q, want %q", name, eb.Error.Code, code)
		}
	}
	if wantStatus != nil && resp.StatusCode != *wantStatus {
		t.Errorf("%s: status = %d, want %d\n%s", name, resp.StatusCode, *wantStatus, body)
	}

	for h, raw := range e.Headers {
		var exact string
		if err := json.Unmarshal(raw, &exact); err == nil {
			if got := resp.Header.Get(h); got != exact {
				t.Errorf("%s: header %s = %q, want %q", name, h, got, exact)
			}
			continue
		}
		var p struct {
			Present bool `json:"present"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			t.Fatalf("%s: bad header expectation for %s", name, h)
		}
		if _, ok := resp.Header[http.CanonicalHeaderKey(h)]; ok != p.Present {
			t.Errorf("%s: header %s present = %v, want %v", name, h, ok, p.Present)
		}
	}

	if e.JSON != nil || len(e.JSONAbsent) > 0 {
		var got any
		if err := json.Unmarshal(body, &got); err != nil {
			t.Errorf("%s: body is not JSON: %v\n%s", name, err, body)
		} else {
			if e.JSON != nil {
				var want any
				if err := json.Unmarshal(e.JSON, &want); err != nil {
					t.Fatalf("%s: bad json expectation: %v", name, err)
				}
				if diff := subset(want, got, ""); diff != "" {
					t.Errorf("%s: %s\nbody: %s", name, diff, body)
				}
			}
			for _, ptr := range e.JSONAbsent {
				if _, err := pointer(got, ptr); err == nil {
					t.Errorf("%s: %s should be absent", name, ptr)
				}
			}
		}
	}

	if e.BodySHA256 != nil {
		sum := sha256.Sum256(body)
		if got := hex.EncodeToString(sum[:]); got != *e.BodySHA256 {
			t.Errorf("%s: body sha256 = %s, want %s", name, got, *e.BodySHA256)
		}
	}
	if e.BodyLength != nil && int64(len(body)) != *e.BodyLength {
		t.Errorf("%s: body length = %d, want %d", name, len(body), *e.BodyLength)
	}
	if e.BodyEmpty != nil && (len(body) == 0) != *e.BodyEmpty {
		t.Errorf("%s: body empty = %v, want %v", name, len(body) == 0, *e.BodyEmpty)
	}
	if e.BodyBytes != nil {
		if !bytes.Equal(body, generate(e.BodyBytes.Seed, e.BodyBytes.Length)) {
			t.Errorf("%s: body does not equal generated bytes (seed %d, length %d)", name, e.BodyBytes.Seed, e.BodyBytes.Length)
		}
	}
}

// --- helpers ---

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(b)
}

func loadErrorTable(t *testing.T) map[string]int {
	var tbl errorTable
	if err := json.Unmarshal([]byte(mustRead(t, filepath.Join(specDir, "errors.json"))), &tbl); err != nil {
		t.Fatalf("errors.json: %v", err)
	}
	m := map[string]int{}
	for _, e := range tbl.Errors {
		m[e.Code] = e.Status
	}
	return m
}

// encode is the RFC 3986 unreserved-set percent-encoder from wire.md section 1.
func encode(s string) string {
	const hexDigits = "0123456789ABCDEF"
	var b strings.Builder
	for _, c := range []byte(s) {
		switch {
		case 'A' <= c && c <= 'Z', 'a' <= c && c <= 'z', '0' <= c && c <= '9', c == '-', c == '.', c == '_', c == '~':
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(hexDigits[c>>4])
			b.WriteByte(hexDigits[c&15])
		}
	}
	return b.String()
}

func substitute(s string, vars map[string]string) string {
	for k, v := range vars {
		s = strings.ReplaceAll(s, "{{"+k+"}}", v)
	}
	return s
}

// generate is the sha256-counter byte generator from fixtures.json.
func generate(seed, length int64) []byte {
	out := make([]byte, 0, length)
	var block [16]byte
	binary.BigEndian.PutUint64(block[:8], uint64(seed))
	for counter := int64(0); int64(len(out)) < length; counter++ {
		binary.BigEndian.PutUint64(block[8:], uint64(counter))
		sum := sha256.Sum256(block[:])
		out = append(out, sum[:]...)
	}
	return out[:length]
}

// subset reports the first place where want is not a subset of got.
func subset(want, got any, at string) string {
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			return fmt.Sprintf("%s: want object, got %T", pathOr(at), got)
		}
		for k, wv := range w {
			gv, ok := g[k]
			if !ok {
				return fmt.Sprintf("%s/%s: missing", at, k)
			}
			if d := subset(wv, gv, at+"/"+k); d != "" {
				return d
			}
		}
		return ""
	default:
		if !reflect.DeepEqual(want, got) {
			return fmt.Sprintf("%s: got %v, want %v", pathOr(at), got, want)
		}
		return ""
	}
}

func pathOr(at string) string {
	if at == "" {
		return "(root)"
	}
	return at
}

// pointer resolves an RFC 6901 JSON pointer against a decoded value.
func pointer(v any, ptr string) (any, error) {
	if ptr == "" {
		return v, nil
	}
	if !strings.HasPrefix(ptr, "/") {
		return nil, fmt.Errorf("pointer %q must start with /", ptr)
	}
	for _, tok := range strings.Split(ptr[1:], "/") {
		tok = strings.ReplaceAll(strings.ReplaceAll(tok, "~1", "/"), "~0", "~")
		switch cur := v.(type) {
		case map[string]any:
			next, ok := cur[tok]
			if !ok {
				return nil, fmt.Errorf("%q not found", tok)
			}
			v = next
		case []any:
			i, err := strconv.Atoi(tok)
			if err != nil || i < 0 || i >= len(cur) {
				return nil, fmt.Errorf("index %q out of range", tok)
			}
			v = cur[i]
		default:
			return nil, fmt.Errorf("cannot descend into %T at %q", v, tok)
		}
	}
	return v, nil
}

func capture(from string, resp *http.Response, body []byte) (string, error) {
	if h, ok := strings.CutPrefix(from, "header:"); ok {
		return resp.Header.Get(h), nil
	}
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return "", err
	}
	val, err := pointer(v, from)
	if err != nil {
		return "", err
	}
	switch x := val.(type) {
	case string:
		return x, nil
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64), nil
	default:
		return fmt.Sprint(x), nil
	}
}
