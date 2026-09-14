package httpapi

import "net/http"

// cors is stage 0 (ADR-0009, spec/wire.md 10): it wraps the whole router,
// outside authentication, because browsers send preflights bare. A
// preflight, OPTIONS with Origin and Access-Control-Request-Method, is
// answered here from configuration alone and never reaches the router: 204
// with the allow headers when the origin matches, a bare 204 otherwise. Any
// other request from a matching origin gets Access-Control-Allow-Origin,
// Access-Control-Expose-Headers: * (valid because nothing here is
// credentialed) and Vary: Origin unless the answer is "*", set before the
// handler runs since headers freeze at the first write. A request with no
// Origin, a non-matching origin, or CORS off passes through untouched.
func (h *handler) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" {
			next.ServeHTTP(w, r)
			return
		}
		value, ok := h.origins.Match(origin)
		preflight := r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != ""
		if preflight {
			if ok {
				w.Header().Set("Access-Control-Allow-Origin", value)
				w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, PUT, POST, PATCH, DELETE")
				headers := r.Header.Get("Access-Control-Request-Headers")
				if headers != "" {
					w.Header().Set("Access-Control-Allow-Headers", headers)
				}
				w.Header().Set("Access-Control-Max-Age", "86400")
				w.Header().Set("Vary", "Origin, Access-Control-Request-Method, Access-Control-Request-Headers")
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", value)
		w.Header().Set("Access-Control-Expose-Headers", "*")
		if value != "*" {
			w.Header().Set("Vary", "Origin")
		}
		next.ServeHTTP(w, r)
	})
}
