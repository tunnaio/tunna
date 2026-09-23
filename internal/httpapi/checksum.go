package httpapi

import (
	"errors"
	"net/http"
)

// errBothChecksums is bodyChecksum's answer when a request declares the
// checksum in both places; the handler turns it into malformed_request.
var errBothChecksums = errors.New("checksum given both as X-Tunna-Checksum and as the checksum query parameter")

// bodyChecksum reads the checksum a request declares for its body: the
// X-Tunna-Checksum header, or the checksum query parameter, which is the
// browser's form because it needs no preflight (spec/wire.md 8, ADR-0006).
// source is the name the value came under, so a malformed value is blamed
// on the right one. An empty value means none was declared.
func bodyChecksum(r *http.Request) (value, source string, err error) {
	query, err := rawQuery(r)
	if err != nil {
		return "", "", err
	}
	header := r.Header.Get("X-Tunna-Checksum")
	param := query.Get("checksum")
	switch {
	case header != "" && param != "":
		return "", "", errBothChecksums
	case header != "":
		return header, "X-Tunna-Checksum", nil
	case param != "":
		return param, "checksum", nil
	}
	return "", "", nil
}
