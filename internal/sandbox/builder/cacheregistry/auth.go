package cacheregistry

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

const bearerScheme = "Bearer "

// tokenAuth rejects any request whose Bearer credential does not match token in
// constant time. The registry is bound to the sandbox gateway, so the token is
// the only access control; it must never be compared with a variable-time
// equality.
func tokenAuth(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		provided, ok := strings.CutPrefix(request.Header.Get("Authorization"), bearerScheme)
		if !ok || subtle.ConstantTimeCompare([]byte(provided), []byte(token)) != 1 {
			writer.Header().Set("WWW-Authenticate", `Bearer realm="agents-sandbox-builder-registry"`)
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(writer, request)
	})
}
