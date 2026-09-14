package httpapi_test

// Conformance runner: replays spec/conformance/cases/*.json against the HTTP
// handler built by httpapi.New, the same constructor cmd/tunna uses.
//
// Requests are signed with package sig using the keys in fixtures.json.
// Every case gets a fresh server with every fixture provisioned (keys,
// buckets, objects with bytes in a real disk store), so cases cannot affect
// each other and a case may mutate a fixture freely. Metadata lives in the
// memory stores here: this suite tests the wire; persistence has its own
// tests in internal/sqlite and cmd/tunna.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tunnaio/tunna"
	"github.com/tunnaio/tunna/internal/disk"
	"github.com/tunnaio/tunna/internal/httpapi"
	"github.com/tunnaio/tunna/internal/memory"
	"github.com/tunnaio/tunna/sig"
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
	Status      *int                       `json:"status"`
	Error       *string                    `json:"error"`
	Headers     map[string]json.RawMessage `json:"headers"`
	JSON        json.RawMessage            `json:"json"`
	JSONPresent []string                   `json:"json_present"`
	JSONAbsent  []string                   `json:"json_absent"`
	BodySHA256  *string                    `json:"body_sha256"`
	BodyLength  *int64                     `json:"body_length"`
	BodyEmpty   *bool                      `json:"body_empty"`
	BodyBytes   *genBytes                  `json:"body_bytes"`
}

type errorTable struct {
	Errors []struct {
		Code   string `json:"code"`
		Status int    `json:"status"`
	} `json:"errors"`
}

type fixtures struct {
	Keys map[string]struct {
		ID       string `json:"id"`
		Secret   string `json:"secret"`
		Disabled bool   `json:"disabled"`
	} `json:"keys"`
	Buckets map[string]struct {
		Name    string `json:"name"`
		Public  bool   `json:"public"`
		Objects []struct {
			Key         string    `json:"key"`
			ContentType string    `json:"content_type"`
			Text        *string   `json:"text"`
			Bytes       *genBytes `json:"bytes"`
		} `json:"objects"`
	} `json:"buckets"`
}

// fixtureObjects writes every fixture object's bytes into blobs and returns
// the metadata rows pointing at them, checksummed the way the server would.
func fixtureObjects(t *testing.T, fx fixtures, blobs tunna.BlobStore, createdAt time.Time) []tunna.Object {
	t.Helper()
	ctx := context.Background()
	table := crc32.MakeTable(crc32.Castagnoli)
	var out []tunna.Object
	for bucketKey, b := range fx.Buckets {
		bucket := b.Name
		if bucket == "" {
			bucket = bucketKey
		}
		for _, o := range b.Objects {
			var data []byte
			switch {
			case o.Text != nil:
				data = []byte(*o.Text)
			case o.Bytes != nil:
				data = generate(o.Bytes.Seed, o.Bytes.Length)
			}
			id, err := blobs.Create(ctx)
			if err != nil {
				t.Fatalf("fixture %s/%s: Create: %v", bucket, o.Key, err)
			}
			if _, err := blobs.Write(ctx, id, 0, bytes.NewReader(data)); err != nil {
				t.Fatalf("fixture %s/%s: Write: %v", bucket, o.Key, err)
			}
			ct := o.ContentType
			if ct == "" {
				ct = "application/octet-stream"
			}
			out = append(out, tunna.Object{
				Bucket: bucket, Key: o.Key, BlobID: id,
				Size: int64(len(data)), ContentType: ct,
				Checksum:  sig.EncodeCRC32C(crc32.Checksum(data, table)),
				CreatedAt: createdAt,
			})
		}
	}
	return out
}

// fixtureBuckets returns the bucket fixtures as the server should hold them
// before any case runs. The fixture key is the bucket name unless overridden.
func fixtureBuckets(fx fixtures, createdAt time.Time) []tunna.Bucket {
	var out []tunna.Bucket
	for key, b := range fx.Buckets {
		name := b.Name
		if name == "" {
			name = key
		}
		out = append(out, tunna.Bucket{Name: name, Public: b.Public, CreatedAt: createdAt})
	}
	return out
}

// auth is the decoded form of a step's "auth" field.
type auth struct {
	None bool
	Key  *struct {
		Key               string   `json:"key"`
		SignedHeaders     []string `json:"signed_headers"`
		TimeOffsetSeconds int64    `json:"time_offset_seconds"`
		CorruptSignature  bool     `json:"corrupt_signature"`
	}
	Presign *struct {
		Key              string   `json:"key"`
		ExpiresInSeconds int64    `json:"expires_in_seconds"`
		SignedHeaders    []string `json:"signed_headers"`
	}
}

func parseAuth(raw json.RawMessage) (auth, error) {
	var a auth
	if len(raw) == 0 || string(raw) == `"none"` {
		a.None = true
		return a, nil
	}
	var probe struct {
		Key     *string         `json:"key"`
		Presign json.RawMessage `json:"presign"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return a, err
	}
	if probe.Presign != nil {
		return a, json.Unmarshal(probe.Presign, &a.Presign)
	}
	if probe.Key != nil {
		return a, json.Unmarshal(raw, &a.Key)
	}
	return a, fmt.Errorf("unrecognised auth %s", raw)
}

// --- the runner ---

func TestConformance(t *testing.T) {
	version := strings.TrimSpace(mustRead(t, filepath.Join(specDir, "VERSION")))
	statusOf := loadErrorTable(t)
	fx := loadFixtures(t)

	files, err := filepath.Glob(filepath.Join(specDir, "conformance", "cases", "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no case files under %s: %v", specDir, err)
	}

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
				if reason := unsupported(c, fx); reason != "" {
					t.Skip(reason)
				}
				// A fresh server per case: fixtures provisioned as fixtures.json
				// describes them, so no case can poison another through shared
				// state, and a case that mutates a fixture is still a valid case.
				srv := newServer(t, fx)
				defer srv.Close()
				runCase(t, srv, c, statusOf, fx)
			})
		}
	}
}

// newServer builds the server under test with every fixture provisioned:
// keys (disabled ones included), buckets, and objects with their bytes in a
// real disk store. Memory stores for metadata, since the conformance suite
// tests the wire, not persistence; the SQLite adapter has its own contract
// tests and cmd/tunna has the restart tests.
func newServer(t *testing.T, fx fixtures) *httptest.Server {
	t.Helper()
	blobs, err := disk.New(t.TempDir())
	if err != nil {
		t.Fatalf("disk.New: %v", err)
	}
	var keys []tunna.APIKey
	for _, k := range fx.Keys {
		keys = append(keys, tunna.APIKey{ID: k.ID, Secret: k.Secret, Disabled: k.Disabled})
	}
	now := time.Now()
	objects := memory.NewObjectStore(fixtureObjects(t, fx, blobs, now))
	return httptest.NewServer(httpapi.New(httpapi.Options{
		ServerVersion: "test",
		Keys:          memory.NewKeyStore(keys),
		Buckets:       memory.NewBucketStore(fixtureBuckets(fx, now)),
		Objects:       objects,
		Uploads:       memory.NewUploadStore(nil, objects),
		Blobs:         blobs,
	}))
}

// unsupported returns a reason to skip a case the harness cannot serve.
// Every fixture kind is provisioned into the server before any case runs,
// so the only reason left is a fixture name the file does not define.
func unsupported(c conformanceCase, fx fixtures) string {
	if c.Fixtures == nil {
		return ""
	}
	for _, name := range *c.Fixtures {
		_, isBucket := fx.Buckets[name]
		_, isKey := fx.Keys[name]
		if !isBucket && !isKey {
			return "unknown fixture " + name
		}
	}
	return ""
}

func runCase(t *testing.T, srv *httptest.Server, c conformanceCase, statusOf map[string]int, fx fixtures) {
	captured := map[string]string{}
	for i, s := range c.Steps {
		name := s.Name
		if name == "" {
			name = fmt.Sprintf("step-%d", i+1)
		}
		sub := func(in string) string { return substitute(in, captured) }

		// Decoded inputs, after substitution.
		segments := make([]string, len(s.Request.Path))
		for i, seg := range s.Request.Path {
			segments[i] = sub(seg)
		}
		query := url.Values{}
		for _, kv := range s.Request.Query {
			query.Add(sub(kv[0]), sub(kv[1]))
		}
		headers := map[string]string{}
		for k, v := range s.Request.Headers {
			headers[k] = sub(v)
		}

		a, err := parseAuth(s.Request.Auth)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		// URL: sig's encoder is the one under test for the path and query,
		// but the runner must not depend on it being right, so the runner
		// encodes with its own copy.
		var path strings.Builder
		if len(segments) == 0 {
			path.WriteString("/")
		}
		for _, seg := range segments {
			path.WriteString("/")
			path.WriteString(encode(seg))
		}
		requestURL := srv.URL + path.String()

		sigReq := sig.Request{Method: s.Request.Method, Path: segments, Query: query, Headers: headers}
		switch {
		case a.Presign != nil:
			key := fixtureKey(t, fx, a.Presign.Key)
			sigReq.SignedHeaders = a.Presign.SignedHeaders
			expires := time.Now().Unix() + a.Presign.ExpiresInSeconds
			requestURL += "?" + sig.PresignQuery(sigReq, key, expires)
		default:
			var parts []string
			for name, values := range query {
				for _, v := range values {
					parts = append(parts, encode(name)+"="+encode(v))
				}
			}
			if len(parts) > 0 {
				requestURL += "?" + strings.Join(parts, "&")
			}
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

		req, err := http.NewRequest(s.Request.Method, requestURL, reqBody)
		if err != nil {
			t.Fatalf("%s: building request: %v", name, err)
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		if s.Request.Body != nil && s.Request.Body.JSON != nil && req.Header.Get("Content-Type") == "" {
			req.Header.Set("Content-Type", "application/json")
			headers["Content-Type"] = "application/json"
		}
		if s.Request.ExpectContinue {
			req.Header.Set("Expect", "100-continue")
		}
		if a.Key != nil {
			key := fixtureKey(t, fx, a.Key.Key)
			sigReq.SignedHeaders = a.Key.SignedHeaders
			ts := time.Now().Unix() + a.Key.TimeOffsetSeconds
			authz := sig.Authorization(sigReq, key, ts)
			if a.Key.CorruptSignature {
				authz = corruptLastHex(authz)
			}
			req.Header.Set("X-Tunna-Date", strconv.FormatInt(ts, 10))
			req.Header.Set("Authorization", authz)
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

	if e.JSON != nil || len(e.JSONPresent) > 0 || len(e.JSONAbsent) > 0 {
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
			for _, ptr := range e.JSONPresent {
				if _, err := pointer(got, ptr); err != nil {
					t.Errorf("%s: %s should be present: %v\nbody: %s", name, ptr, err, body)
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

func loadFixtures(t *testing.T) fixtures {
	var fx fixtures
	if err := json.Unmarshal([]byte(mustRead(t, filepath.Join(specDir, "conformance", "fixtures.json"))), &fx); err != nil {
		t.Fatalf("fixtures.json: %v", err)
	}
	return fx
}

func fixtureKey(t *testing.T, fx fixtures, name string) sig.Key {
	t.Helper()
	k, ok := fx.Keys[name]
	if !ok {
		t.Fatalf("no key fixture %q", name)
	}
	return sig.Key{ID: k.ID, Secret: k.Secret}
}

// corruptLastHex flips the final hex digit of a signature so it is
// well-formed but wrong.
func corruptLastHex(s string) string {
	last := s[len(s)-1]
	if last == '0' {
		return s[:len(s)-1] + "1"
	}
	return s[:len(s)-1] + "0"
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
	case []any:
		g, ok := got.([]any)
		if !ok {
			return fmt.Sprintf("%s: want array, got %T", pathOr(at), got)
		}
		if len(g) != len(w) {
			return fmt.Sprintf("%s: array length %d, want %d", pathOr(at), len(g), len(w))
		}
		for i := range w {
			if d := subset(w[i], g[i], fmt.Sprintf("%s/%d", at, i)); d != "" {
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
