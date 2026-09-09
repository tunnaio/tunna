package sig

import (
	"net/url"
	"slices"
	"strings"
)

const upperHex = "0123456789ABCDEF"

func isUnreserved(c byte) bool {
	return 'A' <= c && c <= 'Z' ||
		'a' <= c && c <= 'z' ||
		'0' <= c && c <= '9' ||
		c == '-' || c == '.' || c == '_' || c == '~'
}

func EncodeSegment(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		if isUnreserved(c) {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(upperHex[c>>4])
		b.WriteByte(upperHex[c&0xF])
	}
	return b.String()
}

func EncodePath(segments []string) string {
	var encoded []string
	for _, s := range segments {
		encoded = append(encoded, EncodeSegment(s))
	}

	return "/" + strings.Join(encoded, "/")
}

type param struct{ name, value string }

func EncodeQuery(params url.Values) string {
	var encoded []param
	for name, values := range params {
		for _, value := range values {
			encoded = append(encoded, param{EncodeSegment(name), EncodeSegment(value)})
		}
	}

	slices.SortFunc(encoded, func(a, b param) int {
		if c := strings.Compare(a.name, b.name); c != 0 {
			return c
		}
		return strings.Compare(a.value, b.value)
	})

	parts := make([]string, 0, len(encoded))
	for _, p := range encoded {
		parts = append(parts, p.name+"="+p.value)
	}

	return strings.Join(parts, "&")
}
