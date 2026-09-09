package sig

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

const (
	Scheme       = "TUNNA1"
	ParamKey     = "x-tunna-key"
	ParamExpires = "x-tunna-expires"
	ParamSig     = "x-tunna-sig"
	ParamHeaders = "x-tunna-headers"
)

type Mode int

const (
	Header Mode = iota
	Presign
)

type Key struct {
	ID, Secret string
}

type Request struct {
	Method        string
	Path          []string
	Query         url.Values
	Headers       map[string]string
	SignedHeaders []string
}

func headerValue(headers map[string]string, name string) (value string, ok bool) {
	for k, v := range headers {
		if strings.EqualFold(k, name) {
			return v, true
		}
	}
	return "", false
}

func signedHeaderNames(req Request) []string {
	names := make([]string, 0, len(req.SignedHeaders))
	for _, h := range req.SignedHeaders {
		names = append(names, strings.ToLower(h))
	}
	slices.Sort(names)
	return names
}

func presignValues(req Request, key Key, expires int64) url.Values {
	query := req.Query.Clone()
	if query == nil {
		query = url.Values{}
	}
	query.Set(ParamKey, key.ID)
	query.Set(ParamExpires, strconv.FormatInt(expires, 10))
	query.Set(ParamHeaders, strings.Join(signedHeaderNames(req), ";"))
	return query
}

func Canonical(req Request, key Key, timestamp int64, mode Mode) string {
	signedHeaders := signedHeaderNames(req)
	signedHeaderStr := strings.Join(signedHeaders, ";")

	var headerBlock []string
	for _, name := range signedHeaders {
		found, ok := headerValue(req.Headers, name)
		if !ok {
			continue
		}
		value := strings.Join(strings.Fields(found), " ")
		headerBlock = append(headerBlock, name+":"+value)
	}

	var queryStr string
	if mode == Header {
		queryStr = EncodeQuery(req.Query)
	} else {
		queryStr = EncodeQuery(presignValues(req, key, timestamp))
	}

	lines := []string{
		Scheme,
		req.Method,
		EncodePath(req.Path),
		queryStr,
		signedHeaderStr,
		strings.Join(headerBlock, "\n"),
		strconv.FormatInt(timestamp, 10),
	}
	return strings.Join(lines, "\n")
}

func Signature(secret, canonical string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(canonical))
	return hex.EncodeToString(mac.Sum(nil))
}

func Authorization(req Request, key Key, timestamp int64) string {
	signature := Signature(key.Secret, Canonical(req, key, timestamp, Header))
	headers := strings.Join(signedHeaderNames(req), ";")
	return Scheme + "-HMAC-SHA256 key=" + key.ID + ", headers=" + headers + ", sig=" + signature
}

func PresignQuery(req Request, key Key, expires int64) string {
	signature := Signature(key.Secret, Canonical(req, key, expires, Presign))
	return EncodeQuery(presignValues(req, key, expires)) + "&" + ParamSig + "=" + signature
}
