package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func overridePILatestURL(url string) func() {
	orig := piDevLatestURL
	piDevLatestURL = url
	return func() { piDevLatestURL = orig }
}

func TestPILatestVersionReadsPiDev(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"version":"0.84.4","packageName":"@earendil-works/pi-coding-agent"}`))
	}))
	t.Cleanup(srv.Close)
	restore := overridePILatestURL(srv.URL)
	t.Cleanup(restore)

	got, err := latestPIVersion(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "0.84.4" {
		t.Errorf("latestPIVersion() = %q, want 0.84.4", got)
	}
}

func TestPILatestVersionEmptyVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"version":""}`))
	}))
	t.Cleanup(srv.Close)
	restore := overridePILatestURL(srv.URL)
	t.Cleanup(restore)

	if _, err := latestPIVersion(context.Background()); err == nil {
		t.Fatal("expected error for empty version")
	}
}

func TestPILatestVersionNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	restore := overridePILatestURL(srv.URL)
	t.Cleanup(restore)

	if _, err := latestPIVersion(context.Background()); err == nil {
		t.Fatal("expected error on non-200 response")
	}
}

func TestPILatestVersionDecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	t.Cleanup(srv.Close)
	restore := overridePILatestURL(srv.URL)
	t.Cleanup(restore)

	if _, err := latestPIVersion(context.Background()); err == nil {
		t.Fatal("expected error on undecodable response")
	}
}
