package cacheregistry

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	testRegistryToken = "secret-token"
	testCacheTag      = "cache"
)

func newTestRegistryHandler(t *testing.T, root string) (http.Handler, func()) {
	t.Helper()
	handler, app, err := newRegistryHandler(t.Context(), registryConfig{Root: root, Token: testRegistryToken})
	if err != nil {
		t.Fatalf("newRegistryHandler: %v", err)
	}
	return handler, func() { _ = app.Shutdown() }
}

func freeTestPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for free port: %v", err)
	}
	defer func() { _ = listener.Close() }()
	return listener.Addr().(*net.TCPAddr).Port
}

func authBearer(request *http.Request, token string) {
	request.Header.Set("Authorization", "Bearer "+token)
}

// pushBlobAndManifest writes a blob and a manifest referencing it. It exercises
// the registry's real upload/manifest API.
func pushBlobAndManifest(t *testing.T, base, token, repository, tag string, blob []byte) {
	t.Helper()
	client := &http.Client{Timeout: 10 * time.Second}
	digest := "sha256:" + sha256Hex(blob)

	startUpload, err := http.NewRequestWithContext(
		t.Context(), http.MethodPost, base+"/v2/"+repository+"/blobs/uploads/", nil,
	)
	if err != nil {
		t.Fatalf("build upload request: %v", err)
	}
	authBearer(startUpload, token)
	uploadResponse, err := client.Do(startUpload)
	if err != nil {
		t.Fatalf("start blob upload: %v", err)
	}
	defer func() { _ = uploadResponse.Body.Close() }()
	if uploadResponse.StatusCode != http.StatusAccepted {
		t.Fatalf("start blob upload status = %d, want 202", uploadResponse.StatusCode)
	}

	location := uploadResponse.Header.Get("Location")
	uploadURL := base + location
	if strings.HasPrefix(location, "http") {
		uploadURL = location
	}
	parsed, err := url.Parse(uploadURL)
	if err != nil {
		t.Fatalf("parse upload location %q: %v", uploadURL, err)
	}
	query := parsed.Query()
	query.Set("digest", digest)
	parsed.RawQuery = query.Encode()

	putBlob, err := http.NewRequestWithContext(t.Context(), http.MethodPut, parsed.String(), bytes.NewReader(blob))
	if err != nil {
		t.Fatalf("build blob put request: %v", err)
	}
	authBearer(putBlob, token)
	putBlob.Header.Set("Content-Type", "application/octet-stream")
	putResponse, err := client.Do(putBlob)
	if err != nil {
		t.Fatalf("put blob: %v", err)
	}
	defer func() { _ = putResponse.Body.Close() }()
	if putResponse.StatusCode != http.StatusCreated {
		t.Fatalf("put blob status = %d, want 201", putResponse.StatusCode)
	}

	manifest := fmt.Sprintf(
		`{"schemaVersion":2,"mediaType":%q,"config":{"mediaType":%q,"digest":%q,"size":%d},"layers":[]}`,
		ociManifestMediaType, ociConfigMediaType, digest, len(blob),
	)
	putManifest, err := http.NewRequestWithContext(
		t.Context(), http.MethodPut, base+"/v2/"+repository+"/manifests/"+tag, strings.NewReader(manifest),
	)
	if err != nil {
		t.Fatalf("build manifest request: %v", err)
	}
	authBearer(putManifest, token)
	putManifest.Header.Set("Content-Type", ociManifestMediaType)
	manifestResponse, err := client.Do(putManifest)
	if err != nil {
		t.Fatalf("put manifest: %v", err)
	}
	defer func() { _ = manifestResponse.Body.Close() }()
	if manifestResponse.StatusCode != http.StatusCreated {
		t.Fatalf("put manifest status = %d, want 201", manifestResponse.StatusCode)
	}
}

// manifestStatus returns the HTTP status of a manifest GET for the given
// repository/tag.
func manifestStatus(t *testing.T, base, repository string) int {
	t.Helper()
	request, err := http.NewRequestWithContext(
		t.Context(), http.MethodGet, base+"/v2/"+repository+"/manifests/"+testCacheTag, nil,
	)
	if err != nil {
		t.Fatalf("build manifest get request: %v", err)
	}
	authBearer(request, testRegistryToken)
	request.Header.Set("Accept", ociManifestMediaType)
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		t.Fatalf("get manifest: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, response.Body)
	return response.StatusCode
}

// startFakeRegistry binds a token-authenticated /v2/ responder on host:port and
// returns a stop function. It stands in for the detached child process in
// Ensure tests.
func startFakeRegistry(t *testing.T, host string, port int, token string) (func(), error) {
	t.Helper()
	listener, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return nil, err
	}
	handler := tokenAuth(token, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	}))
	server := &http.Server{Handler: handler, ReadHeaderTimeout: time.Second}
	go func() { _ = server.Serve(listener) }()
	return func() { _ = server.Close() }, nil
}

func stubRegistryBind(t *testing.T, host string, port int) {
	t.Helper()
	originalHost, originalPort := registryBindHost, registryBindPort
	registryBindHost = func() string { return host }
	registryBindPort = func() int { return port }
	t.Cleanup(func() {
		registryBindHost, registryBindPort = originalHost, originalPort
	})
}

func stubSpawnRegistry(t *testing.T, spawn registrySpawnFunc) func() {
	t.Helper()
	original := spawnRegistry
	spawnRegistry = spawn
	return func() { spawnRegistry = original }
}

func listCatalogForTest(t *testing.T, base, token string) []string {
	t.Helper()
	parsed, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse base url %q: %v", base, err)
	}
	port := 0
	for _, digit := range parsed.Port() {
		port = port*10 + int(digit-'0')
	}
	repositories, err := listCatalog(t.Context(), Endpoint{Host: parsed.Hostname(), Port: port, Token: token})
	if err != nil {
		t.Fatalf("listCatalog: %v", err)
	}
	return repositories
}
