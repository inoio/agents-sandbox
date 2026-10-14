package cacheregistry

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTokenAuthAllowsAnonymousRead(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(method, "/v2/", nil) // no Authorization header
		ok := false
		tokenAuth(
			"secret",
			http.HandlerFunc(func(http.ResponseWriter, *http.Request) { ok = true }),
		).ServeHTTP(rec, req)
		if !ok || rec.Code == http.StatusUnauthorized {
			t.Fatalf("%s read rejected: code=%d", method, rec.Code)
		}
	}
}

func TestTokenAuthRequiresTokenForWrites(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(method, "/v2/foo/manifests/x", nil) // no token
		tokenAuth("secret", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s without token: code=%d, want 401", method, rec.Code)
		}
	}
}
