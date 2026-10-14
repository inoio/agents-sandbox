package cacheregistry

import (
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestBuildRegistryConfigurationEnablesBlobDescriptorCache(t *testing.T) {
	config, err := buildRegistryConfiguration(t.TempDir())
	if err != nil {
		t.Fatalf("buildRegistryConfiguration: %v", err)
	}
	cache, ok := config.Storage["cache"]
	if !ok {
		t.Fatal("registry config missing the storage cache section")
	}
	if cache["blobdescriptor"] != "inmemory" {
		t.Errorf("blobdescriptor = %v, want inmemory", cache["blobdescriptor"])
	}
}

func TestServeRejectsMissingOrWrongToken(t *testing.T) {
	handler, cleanup := newTestRegistryHandler(t, t.TempDir())
	defer cleanup()
	server := httptest.NewServer(handler)
	defer server.Close()

	tests := []struct {
		name  string
		token string
		want  int
	}{
		{name: "missing", token: "", want: http.StatusUnauthorized},
		{name: "wrong", token: "not-the-token", want: http.StatusUnauthorized},
		{name: "correct", token: testRegistryToken, want: http.StatusOK},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/v2/", nil)
			if err != nil {
				t.Fatalf("build request: %v", err)
			}
			if test.token != "" {
				authBearer(request, test.token)
			}
			response, err := server.Client().Do(request)
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			defer func() { _ = response.Body.Close() }()
			if response.StatusCode != test.want {
				t.Fatalf("status = %d, want %d", response.StatusCode, test.want)
			}
			if test.want == http.StatusUnauthorized && response.Header.Get("WWW-Authenticate") == "" {
				t.Error("401 response missing WWW-Authenticate header")
			}
		})
	}
}

func TestServeRoundTripStoresBlob(t *testing.T) {
	root := t.TempDir()
	blob := []byte("cache-blob-contents")

	firstHandler, firstCleanup := newTestRegistryHandler(t, root)
	firstServer := httptest.NewServer(firstHandler)
	pushBlobAndManifest(t, firstServer.URL, testRegistryToken, "agents-sandbox/project", "cache", blob)
	if status := manifestStatus(t, firstServer.URL, "agents-sandbox/project"); status != http.StatusOK {
		t.Fatalf("manifest status after push = %d, want 200", status)
	}
	firstServer.Close()
	firstCleanup()

	secondHandler, secondCleanup := newTestRegistryHandler(t, root)
	defer secondCleanup()
	secondServer := httptest.NewServer(secondHandler)
	defer secondServer.Close()
	if status := manifestStatus(t, secondServer.URL, "agents-sandbox/project"); status != http.StatusOK {
		t.Fatalf("manifest status after restart = %d, want 200 (blob not persisted)", status)
	}
}

func TestServeIdleReaps(t *testing.T) {
	t.Run("quiet from start", func(t *testing.T) {
		configPath, address := writeIdleTestConfig(t, 200*time.Millisecond)
		done := make(chan error, 1)
		go func() { done <- Serve(t.Context(), configPath) }()

		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("Serve returned error: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("Serve did not reap after idle timeout (address %s)", address)
		}
	})

	t.Run("activity then quiet", func(t *testing.T) {
		configPath, address := writeIdleTestConfig(t, 200*time.Millisecond)
		done := make(chan error, 1)
		go func() { done <- Serve(t.Context(), configPath) }()

		waitForListener(t, address)
		request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+address+"/v2/", nil)
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		authBearer(request, testRegistryToken)
		response, err := (&http.Client{Timeout: time.Second}).Do(request)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", response.StatusCode)
		}

		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("Serve returned error: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("Serve did not reap after activity then quiet (address %s)", address)
		}
	})
}

func writeIdleTestConfig(t *testing.T, idleTimeout time.Duration) (string, string) {
	t.Helper()
	port := freeTestPort(t)
	configPath := filepath.Join(t.TempDir(), "registry-config.json")
	cfg := registryConfig{
		Root:        filepath.Join(t.TempDir(), "cache"),
		Host:        "127.0.0.1",
		Port:        port,
		Token:       testRegistryToken,
		IdleTimeout: idleTimeout,
	}
	if err := writeRegistryConfig(configPath, cfg); err != nil {
		t.Fatalf("writeRegistryConfig: %v", err)
	}
	return configPath, net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
}

func waitForListener(t *testing.T, address string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("registry did not start listening on %s", address)
}
