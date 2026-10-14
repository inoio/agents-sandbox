package cacheregistry

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
)

func TestRegistryHostIncludesPort(t *testing.T) {
	endpoint := Endpoint{Host: GuestHost, Port: 5000}
	if got, want := endpoint.RegistryHost(), GuestHost+":5000"; got != want {
		t.Errorf("RegistryHost() = %q, want %q", got, want)
	}
}

func TestCacheRefDeterministicAndDistinct(t *testing.T) {
	endpoint := Endpoint{Host: GuestHost, Port: 5000, Token: "t"}

	first := endpoint.CacheRef("proj-a/opencode:1.2.3", "linux/amd64")
	second := endpoint.CacheRef("proj-a/opencode:1.2.3", "linux/amd64")
	if first != second {
		t.Fatalf("CacheRef not deterministic: %q vs %q", first, second)
	}
	if !strings.HasPrefix(first, endpoint.RegistryHost()+"/agents-sandbox/") {
		t.Errorf("CacheRef %q missing registry host/repository prefix", first)
	}
	if !strings.HasSuffix(first, "-amd64:cache") {
		t.Errorf("CacheRef %q missing platform arch/cache suffix", first)
	}

	distinct := map[string]string{
		"other project":  endpoint.CacheRef("proj-b/opencode:1.2.3", "linux/amd64"),
		"other agent":    endpoint.CacheRef("proj-a/pi:1.2.3", "linux/amd64"),
		"other platform": endpoint.CacheRef("proj-a/opencode:1.2.3", "linux/arm64"),
		"other version":  endpoint.CacheRef("proj-a/opencode:2.0.0", "linux/amd64"),
	}
	for name, ref := range distinct {
		if ref == first {
			t.Errorf("%s produced the same ref as the baseline: %q", name, ref)
		}
	}
}

func TestEndpointRefsShareRepository(t *testing.T) {
	e := Endpoint{Host: GuestHost, Port: 5000, dialHost: "127.0.0.1"}
	if got := e.PushRef(
		"agents-sandbox/runner-p:opencode-latest",
	); got != "host.microsandbox.internal:5000/agents-sandbox/runner-p:opencode-latest" {
		t.Fatalf("PushRef = %q", got)
	}
	if got := e.PullRef(
		"agents-sandbox/runner-p:opencode-latest",
	); got != "127.0.0.1:5000/agents-sandbox/runner-p:opencode-latest" {
		t.Fatalf("PullRef = %q", got)
	}
}

func TestPruneDeletesMatchingRepos(t *testing.T) {
	root := t.TempDir()
	handler, cleanup := newTestRegistryHandler(t, root)
	defer cleanup()
	server := httptest.NewServer(handler)
	defer server.Close()

	for _, repository := range []string{"agents-sandbox/repo-a", "agents-sandbox/repo-b", "unrelated/repo-c"} {
		pushBlobAndManifest(t, server.URL, testRegistryToken, repository, "cache", []byte(repository))
	}

	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse server url: %v", err)
	}
	endpoint := Endpoint{Host: parsed.Hostname(), Port: mustPort(t, parsed), Token: testRegistryToken, root: root}

	matched := func(reference string) bool { return strings.HasPrefix(reference, "agents-sandbox/") }
	if err := Prune(t.Context(), endpoint, matched); err != nil {
		t.Fatalf("Prune: %v", err)
	}

	for _, repository := range []string{"agents-sandbox/repo-a", "agents-sandbox/repo-b"} {
		if status := manifestStatus(t, server.URL, repository); status != http.StatusNotFound {
			t.Errorf("%s manifest status after prune = %d, want 404", repository, status)
		}
	}
	if status := manifestStatus(t, server.URL, "unrelated/repo-c"); status != http.StatusOK {
		t.Errorf("unrelated manifest status after prune = %d, want 200", status)
	}

	catalog := listCatalogForTest(t, server.URL, testRegistryToken)
	for _, repository := range catalog {
		if strings.HasPrefix(repository, "agents-sandbox/") {
			t.Errorf("catalog still lists pruned repository %q", repository)
		}
	}
	if !contains(catalog, "unrelated/repo-c") {
		t.Errorf("catalog %v missing unrelated repository", catalog)
	}
}

func mustPort(t *testing.T, parsed *url.URL) int {
	t.Helper()
	port := parsed.Port()
	if port == "" {
		t.Fatal("test server url has no port")
	}
	value := 0
	for _, digit := range port {
		value = value*10 + int(digit-'0')
	}
	return value
}

func contains(values []string, target string) bool {
	return slices.Contains(values, target)
}
