package cacheregistry

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

const bearerScheme = "Bearer "

// tokenAuth permits anonymous reads (GET/HEAD) and rejects any other request
// whose Bearer credential does not match token in constant time. The registry
// binds loopback only, and msb cannot present a bearer token; the token guards
// writes (cache poisoning) rather than reads, so pulls and BuildKit blob
// existence checks are unauthenticated. The token must never be compared with a
// variable-time equality.
func tokenAuth(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet || request.Method == http.MethodHead {
			next.ServeHTTP(writer, request)
			return
		}
		provided, ok := strings.CutPrefix(request.Header.Get("Authorization"), bearerScheme)
		if !ok || subtle.ConstantTimeCompare([]byte(provided), []byte(token)) != 1 {
			writer.Header().Set("WWW-Authenticate", `Bearer realm="agents-sandbox-builder-registry"`)
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(writer, request)
	})
}
