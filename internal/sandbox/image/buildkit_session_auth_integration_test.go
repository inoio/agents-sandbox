//go:build integration

package image

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/docker/cli/cli/config"
	"github.com/moby/moby/api/types/build"
	"github.com/moby/moby/client"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
)

const (
	probeRegistryUser     = "probe-user"
	probeRegistryPassword = "probe-password"
	probeRegistryToken    = "probe-bearer-token"
	probeRegistryRepo     = "probe/base"
)

// probeRegistryCredential is one credential the registry's token endpoint was
// asked to authenticate.
type probeRegistryCredential struct {
	user, password string
}

// probeRegistry is a minimal authenticated registry serving one private image.
// Its token endpoint refuses any request that carries no credential, so the
// image only resolves when the BuildKit session carried the docker config's
// auth into the daemon's resolver; it also records what each token request
// presented, so a failure names the credential that was (not) delivered.
type probeRegistry struct {
	server *httptest.Server

	mu          sync.Mutex
	credentials []probeRegistryCredential
}

func newProbeRegistry(t *testing.T) *probeRegistry {
	t.Helper()
	registry := &probeRegistry{}
	registry.server = httptest.NewServer(http.HandlerFunc(registry.handle))
	t.Cleanup(registry.server.Close)
	return registry
}

// host is the registry endpoint as an image reference sees it, e.g.
// "127.0.0.1:34567".
func (r *probeRegistry) host() string {
	return strings.TrimPrefix(r.server.URL, "http://")
}

func (r *probeRegistry) authChallenge() string {
	return fmt.Sprintf(
		`Bearer realm="%s/token",service="probe",scope="repository:%s:pull"`,
		r.server.URL,
		probeRegistryRepo,
	)
}

// imageConfig is an empty-rootfs image (the registry-side equivalent of
// `FROM scratch`), which is enough for the `COPY` the probe Dockerfile adds.
func imageConfig() []byte {
	return []byte(`{"architecture":"amd64","os":"linux","config":{},"rootfs":{"type":"layers","diff_ids":[]}}`)
}

func imageManifest(configDigest string, configSize int) []byte {
	return []byte(fmt.Sprintf(
		`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json",`+
			`"config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":%q,"size":%d},`+
			`"layers":[]}`,
		configDigest, configSize,
	))
}

func (r *probeRegistry) handle(w http.ResponseWriter, req *http.Request) {
	switch {
	case req.URL.Path == "/token":
		r.serveToken(w, req)
	case req.URL.Path == "/v2/" || req.URL.Path == "/v2":
		_, _ = w.Write([]byte("{}"))
	case req.Header.Get("Authorization") == "Bearer "+probeRegistryToken:
		r.serveAuthorized(w, req)
	default:
		w.Header().Set("WWW-Authenticate", r.authChallenge())
		http.Error(w, "authentication required", http.StatusUnauthorized)
	}
}

// serveToken issues a token only to a request that presents a credential. The
// client-side auth provider sends one as HTTP basic auth on the GET fallback and
// as form fields on the OAuth POST (containerd's FetchTokenWithOAuth carries no
// Authorization header), so both are read.
func (r *probeRegistry) serveToken(w http.ResponseWriter, req *http.Request) {
	credential := probeRegistryCredential{}
	if user, password, ok := req.BasicAuth(); ok {
		credential = probeRegistryCredential{user: user, password: password}
	} else if err := req.ParseForm(); err == nil {
		credential = probeRegistryCredential{
			user:     req.PostForm.Get("username"),
			password: req.PostForm.Get("password"),
		}
		if credential.user == "" {
			credential.password = req.PostForm.Get("refresh_token")
		}
	}

	r.mu.Lock()
	r.credentials = append(r.credentials, credential)
	r.mu.Unlock()

	if credential.user == "" && credential.password == "" {
		http.Error(w, "credentials required", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprintf(w, `{"token":%q,"access_token":%q}`, probeRegistryToken, probeRegistryToken)
}

func (r *probeRegistry) serveAuthorized(w http.ResponseWriter, req *http.Request) {
	imageConfig := imageConfig()
	configDigest := fmt.Sprintf("sha256:%x", sha256.Sum256(imageConfig))
	switch {
	case strings.Contains(req.URL.Path, "/manifests/"):
		manifest := imageManifest(configDigest, len(imageConfig))
		w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
		w.Header().Set("Docker-Content-Digest", fmt.Sprintf("sha256:%x", sha256.Sum256(manifest)))
		if req.Method != http.MethodHead {
			_, _ = w.Write(manifest)
		}
	case strings.HasSuffix(req.URL.Path, "/blobs/"+configDigest):
		w.Header().Set("Content-Type", "application/vnd.oci.image.config.v1+json")
		_, _ = w.Write(imageConfig)
	default:
		http.NotFound(w, req)
	}
}

// presentedCredentials returns the credential of each token request the
// registry saw.
func (r *probeRegistry) presentedCredentials() []probeRegistryCredential {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]probeRegistryCredential(nil), r.credentials...)
}

// testProbeRegistryConfig points the launcher's docker config at a directory
// holding the registry's credential, and restores it when the test ends.
//
// The directory goes through config.SetDir, not t.Setenv: config.Dir() memoizes
// DOCKER_CONFIG in a sync.Once, so a t.Setenv would leave the process pinned to
// this test's directory (which t.TempDir then removes) and silently strip the
// credential from every later test's session.
func testProbeRegistryConfig(t *testing.T, host string) {
	t.Helper()
	dir := t.TempDir()
	auth := base64.StdEncoding.EncodeToString([]byte(probeRegistryUser + ":" + probeRegistryPassword))
	body := `{"auths":{"` + host + `":{"auth":"` + auth + `"}}}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	originalDir := config.Dir()
	config.SetDir(dir)
	t.Cleanup(func() { config.SetDir(originalDir) })
}

// environmentDockerConfigDir is the config directory DOCKER_CONFIG/$HOME
// resolve to right now — what config.Dir() returns when nothing has pinned it.
// It mirrors docker/cli's own resolution.
func environmentDockerConfigDir() string {
	if dir := os.Getenv("DOCKER_CONFIG"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".docker")
}

// TestDockerConfigDirIsNotPinnedByOtherTests pins that no earlier test left
// config.Dir() pointing at a directory of its own.
//
// config.Dir() memoizes DOCKER_CONFIG — and, through getHomeDir, $HOME — in a
// package-level sync.Once, so a test that sets either with t.Setenv before the
// process's first config.Dir() call pins it (under a t.TempDir, which t.TempDir
// then removes) for every later test. That silently strips the registry
// credential from every integration build's BuildKit session.
//
// It has to run after the rest of the package's tests; the filename sorts after
// buildkit_build_test.go and helpers_test.go, the files that set the two
// variables.
func TestDockerConfigDirIsNotPinnedByOtherTests(t *testing.T) {
	want := environmentDockerConfigDir()
	if want == "" {
		t.Skip("no environment-derived docker config dir to compare against")
	}
	if got := config.Dir(); got != want {
		t.Fatalf(
			"config.Dir() = %q, want %q: an earlier test pinned it with t.Setenv instead of restoring it through config.SetDir",
			got,
			want,
		)
	}
}

// TestBuildKitSessionAuthPullsPrivateBaseImage pins the end-to-end session
// registry-auth path: the docker config's credential for the base image's
// registry must travel client -> BuildKit session -> daemon resolver, so a
// private base image pulls.
//
// It guards the whole-class failure in which one test pinned config.Dir() and
// every integration build then resolved its registries anonymously (the daemon
// fell back to an anonymous token and the registry rejected it — for GHCR, a
// misleading `failed to fetch anonymous token`). It has to run after the rest
// of the package's tests for that reason; the filename sorts after
// buildkit_build_test.go, whose TestNewBuildKitSessionProvidesRegistryAuth was
// the pinning test.
func TestBuildKitSessionAuthPullsPrivateBaseImage(t *testing.T) {
	origGet := docker.Get
	docker.Get = docker.RealClient
	t.Cleanup(func() { docker.Get = origGet })

	ctx := context.Background()
	ping, err := docker.Get().Ping(ctx, client.PingOptions{})
	if err != nil {
		t.Skipf("docker daemon not available: %v", err)
	}
	if ping.BuilderVersion != build.BuilderBuildKit {
		t.Skipf("engine does not advertise BuildKit (Builder-Version=%q)", ping.BuilderVersion)
	}

	registry := newProbeRegistry(t)
	testProbeRegistryConfig(t, registry.host())

	a, ok := agent.Lookup("opencode")
	if !ok {
		t.Fatal("opencode agent not registered")
	}

	tag := "agents-sandbox/it-session-auth"
	t.Cleanup(func() { removeTestImage(tag) })

	dockerfile := []byte("FROM " + registry.host() + "/" + probeRegistryRepo + ":latest\nCOPY Dockerfile /copied\n")
	req := testBuildRequest(dockerfile, "", "", "", imageIdentity{})
	req.cacheFrom, req.cacheTo = imageCacheEnv(sanitizeTag(tag))

	var lines []string
	buildErr := buildImageBuildKit(
		ctx,
		docker.Get(),
		a,
		req,
		tag,
		false,
		func(s string) { lines = append(lines, s) },
	)
	if buildErr != nil {
		t.Fatalf("private base image build failed: %v\noutput:\n%s", buildErr, strings.Join(lines, "\n"))
	}
	if _, err := docker.Get().ImageInspect(ctx, tag); err != nil {
		t.Fatalf("inspect the built image: %v", err)
	}

	credentials := registry.presentedCredentials()
	if len(credentials) == 0 {
		t.Fatalf("the registry served no token request; the base image resolved without reaching it")
	}
	want := probeRegistryCredential{user: probeRegistryUser, password: probeRegistryPassword}
	for _, got := range credentials {
		if got != want {
			t.Fatalf(
				"registry token credential = %+v, want %+v: the session did not carry the docker config's credential",
				got,
				want,
			)
		}
	}
}
