package docker

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/moby/moby/client"
)

// TestRealClientImageListClientError pins the shared-client construction failure
// branch every real-client method guards on: when the moby client cannot be
// created, ImageList surfaces the error instead of dereferencing a nil client.
// The ensureMobyClient seam lets the branch be reached without a daemon.
func TestRealClientImageListClientError(t *testing.T) {
	wantErr := errors.New("no docker")
	orig := ensureMobyClient
	ensureMobyClient = func() error { return wantErr }
	t.Cleanup(func() { ensureMobyClient = orig })

	_, err := (realDockerClient{}).ImageList(context.Background(), client.ImageListOptions{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("ImageList error = %v, want %v", err, wantErr)
	}
}

// TestRealClientImageListSuccess pins the delegation line: with a usable client,
// ImageList returns the daemon's response. A stub HTTP server stands in for the
// daemon so the success path is covered without a real Docker install.
func TestRealClientImageListSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	}))
	t.Cleanup(server.Close)

	mobyClientForTest, err := client.New(client.WithHost(server.URL))
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	origEnsure := ensureMobyClient
	origClient := mobyClient
	ensureMobyClient = func() error {
		mobyClient = mobyClientForTest
		return nil
	}
	t.Cleanup(func() {
		ensureMobyClient = origEnsure
		mobyClient = origClient
	})

	result, err := (realDockerClient{}).ImageList(context.Background(), client.ImageListOptions{})
	if err != nil {
		t.Fatalf("ImageList: %v", err)
	}
	if len(result.Items) != 0 {
		t.Errorf("ImageList items = %v, want none", result.Items)
	}
}
