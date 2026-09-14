package tunna

import (
	"fmt"
	"net/url"
	"strings"
)

// origin is one normalised allowlist entry: scheme, lowercase host, port
// with the scheme's default dropped, and whether the host was "*.domain".
type origin struct {
	scheme   string
	host     string
	port     string
	wildcard bool
}

// Origins is a parsed CORS allowlist (ADR-0009). The zero value allows
// nothing, which is how CORS is switched off. It is not a security
// boundary: every request carries an explicit credential, so the list only
// decides which pages a browser lets read the responses.
type Origins struct {
	all     bool
	entries []origin
}

// normalize renders a parsed origin the way a browser sends one: lowercase
// host, and no port when it is the scheme's default. Used on both the
// configured entries and the request, so the two cannot drift.
func normalize(u *url.URL) (host, port string) {
	host = strings.ToLower(u.Hostname())
	port = u.Port()
	if (u.Scheme == "http" && port == "80") || (u.Scheme == "https" && port == "443") {
		port = ""
	}
	return host, port
}

// ParseOrigins validates and normalises configured entries: "*", an exact
// scheme://host[:port], or scheme://*.domain[:port] whose "*" stands for
// exactly one DNS label. Scheme is http or https; a path, query, fragment,
// userinfo, or a "*" anywhere but the whole leftmost label is ErrInvalid,
// and one bad entry fails the list so a typo cannot silently drop an
// origin. spec/vectors/cors.json is the definition.
func ParseOrigins(entries []string) (Origins, error) {
	origins := Origins{}
	for _, e := range entries {
		trimmed := strings.TrimSpace(e)
		if trimmed == "*" {
			origins.all = true
			continue
		}
		u, err := url.Parse(trimmed)
		if err != nil {
			return Origins{}, fmt.Errorf("%w: cors origin %q: %v", ErrInvalid, trimmed, err)
		}
		switch {
		case u.Scheme != "http" && u.Scheme != "https":
			return Origins{}, fmt.Errorf("%w: cors origin %q: scheme must be http or https", ErrInvalid, trimmed)
		case u.Host == "" || u.Hostname() == "":
			return Origins{}, fmt.Errorf("%w: cors origin %q: no host", ErrInvalid, trimmed)
		case u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil:
			return Origins{}, fmt.Errorf("%w: cors origin %q: an origin is scheme://host[:port] and nothing more", ErrInvalid, trimmed)
		}
		host, port := normalize(u)
		rest, wildcard := strings.CutPrefix(host, "*.")
		if wildcard {
			host = rest
		}
		if host == "" || strings.Contains(host, "*") {
			return Origins{}, fmt.Errorf("%w: cors origin %q: * may only be the whole leftmost label", ErrInvalid, trimmed)
		}
		o := origin{
			host:     host,
			port:     port,
			scheme:   u.Scheme,
			wildcard: wildcard,
		}
		origins.entries = append(origins.entries, o)
	}
	return origins, nil
}

// Match reports whether a request's Origin header is allowed and, if so,
// the Access-Control-Allow-Origin value to answer with: "*" when the list
// holds "*", otherwise the request origin unchanged, byte for byte. The
// literal "null" and anything that is not an http or https origin match
// only "*". An empty origin never matches.
func (o Origins) Match(requestOrigin string) (value string, ok bool) {
	if requestOrigin == "" {
		return "", false
	}
	if o.all {
		return "*", true
	}
	u, err := url.Parse(requestOrigin)
	if err != nil {
		return "", false
	}
	switch {
	case u.Scheme != "http" && u.Scheme != "https":
		return "", false
	case u.Host == "" || u.Hostname() == "":
		return "", false
	case u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil:
		return "", false
	}
	host, port := normalize(u)
	for _, e := range o.entries {
		if e.scheme != u.Scheme || e.port != port {
			continue
		}
		if !e.wildcard {
			if host == e.host {
				return requestOrigin, true
			}
			continue
		}
		label, ok := strings.CutSuffix(host, "."+e.host)
		if ok && label != "" && !strings.Contains(label, ".") {
			return requestOrigin, true
		}
	}
	return "", false
}

// String renders the normalised list for the startup log, so the log shows
// what the server understood rather than what the operator typed. It
// satisfies fmt.Stringer, which slog uses when given the value directly.
func (o Origins) String() string {
	parts := make([]string, 0, len(o.entries)+1)
	if o.all {
		parts = append(parts, "*")
	}
	for _, e := range o.entries {
		host := e.host
		if e.wildcard {
			host = "*." + host
		}
		if e.port != "" {
			host += ":" + e.port
		}
		parts = append(parts, e.scheme+"://"+host)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
