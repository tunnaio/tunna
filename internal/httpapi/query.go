package httpapi

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// rawQuery parses r.URL.RawQuery with url.PathUnescape, so a literal '+'
// stays a '+'. r.URL.Query() would turn it into a space, and base64 values
// such as a checksum carry '+'. The signature check and the body checksum
// both read the query through it.
func rawQuery(r *http.Request) (url.Values, error) {
	query := url.Values{}
	for pair := range strings.SplitSeq(r.URL.RawQuery, "&") {
		if pair == "" {
			continue
		}
		name, val, _ := strings.Cut(pair, "=")
		n, err1 := url.PathUnescape(name)
		v, err2 := url.PathUnescape(val)
		if err1 != nil || err2 != nil {
			return nil, errors.Join(err1, err2)
		}
		query.Add(n, v)
	}
	return query, nil
}
