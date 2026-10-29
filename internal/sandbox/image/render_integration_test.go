//go:build integration

package image

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/moby/moby/client"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/docker"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
)

// integrationAgentVersion returns the release the integration image builds
// should bake. It prefers the launcher's real upgrade cache (the machine-global
// latest release the upgrade prompt offers), so a machine that has used the
// agent needs no network. When the cache is empty — a fresh CI runner — it
// resolves the release once through the agent's own latest-release check and
// records it in the real root state, so the rest of the suite reuses it.
func integrationAgentVersion(t *testing.T, a agent.Agent) string {
	t.Helper()
	configpaths.WithRealConfigPaths(t)

	if version, cached := cachedAgentVersion(a); cached {
		return version
	}

	version, err := agent.LatestVersion(context.Background(), a)
	if err != nil {
		t.Fatalf("resolve %s version: %v", a.Name(), err)
	}
	recordCachedAgentVersion(t, a, version)
	return version
}

// recordCachedAgentVersion stores version as the agent's cached latest release so
// a later integration test does not resolve it again. It preserves the existing
// last_checked, so it does not suppress the developer's own upgrade check. A
// write failure only logs: the test has its version either way.
func recordCachedAgentVersion(t *testing.T, a agent.Agent, version string) {
	t.Helper()
	if _, err := state.WithRootState(true, func(tx *state.RootTxn) error {
		check := tx.State().AgentCheck(a.Name())
		check.LatestVersion = version
		tx.State().SetAgentCheck(a.Name(), check)
		return tx.Commit()
	}); err != nil {
		t.Logf("could not cache the %s release %q in the launcher root state: %v", a.Name(), version, err)
	}
}

// TestRenderDockerfileBuilds builds a real image for the Dockerfile composition
// cases TestRenderDockerfileBuildsEveryAgent does not cover: the
// docker-enablement surface (config, marker, and the implied base-ref/dind
// forms) and a custom tar-less base (#143). These run real docker builds and are
// excluded from the main suite via the `integration` build tag; run with
// `make test-integration`. They require a working docker daemon and network
// access (base image pulls and the node/docker/agent installs baked into the
// rendered Dockerfile).
//
// They exist to give refactoring safety for the per-tool multistage composition
// in dockerfile.go RenderDockerfile: each case renders a Dockerfile and asserts
// the whole thing actually builds. The custom-base cases use Fedora (dnf) so
// they exercise a non-Debian distro; the docker cases install the engine's
// runtime prerequisites in the body, which the final-stage check must accept.
func TestRenderDockerfileBuilds(t *testing.T) {
	origGet := docker.Get
	docker.Get = docker.RealClient
	t.Cleanup(func() { docker.Get = origGet })

	a, ok := agent.Lookup("opencode")
	if !ok {
		t.Fatal("opencode agent not registered")
	}

	version := integrationAgentVersion(t, a)

	cases := []struct {
		name              string
		projectDockerfile []byte
		docker            options.DockerSetting
	}{
		{"no-dockerfile-docker", nil, dockerOn},
		{"managed-base", []byte("FROM agents-sandbox/runner-base:latest\nRUN echo managed\n"), dockerOff},
		{
			"managed-base-docker-implied",
			[]byte("FROM agents-sandbox/runner-base-docker:latest\nRUN echo managed\n"),
			dockerOff,
		},
		{
			"managed-base-dind-implied",
			[]byte("FROM agents-sandbox/runner-base-dind:latest\nRUN echo managed\n"),
			dockerOff,
		},
		// #143: the image build's downloads run on the tool base, so a custom
		// base needs no tar; curl is installed only because the daemon agent's
		// in-VM runtime tool (curl) is checked at build time.
		{"custom-base-slim-no-tar", []byte(
			"FROM debian:trixie-slim\n" +
				"RUN apt-get update && apt-get install -y --no-install-recommends curl procps && rm -rf /var/lib/apt/lists/*\n"), dockerOff},
		{"custom-base-slim-no-tar-docker", []byte(
			"FROM debian:trixie-slim\n" +
				"RUN apt-get update && apt-get install -y --no-install-recommends curl procps iptables git xz-utils && rm -rf /var/lib/apt/lists/*\n"), dockerOn},
		{"custom-base-fedora-docker", []byte(
			"FROM fedora:latest\n" +
				"RUN dnf install -y curl tar iptables git procps-ng xz && dnf clean all\n"), dockerOn},
		{"custom-base-docker-marker", []byte(
			"FROM fedora:latest\n" +
				"RUN dnf install -y curl tar iptables git procps-ng xz && dnf clean all\n" +
				"# agents-sandbox:docker\n" +
				"RUN echo after-docker\n"), dockerOn},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rendered := renderBytesVersion(t, a, version, tc.projectDockerfile, tc.docker)
			buildDockerfile(t, a, rendered, version, tc.docker.Enabled)
		})
	}
}

// TestRenderDockerfileBuildsEveryAgent builds and runs every registered agent
// profile on the launcher's managed base (a nil project Dockerfile; the sibling
// test's `managed-base` case is the explicit runner-base ref instead) and on a
// custom non-Debian base. The agent stage always builds on the Debian tool base,
// so the custom base exercises the final-stage merge and the agent's *runtime*,
// not its install: running the version probe in the built image is what catches
// a broken binary or a runtime library the discarded agent stage installed.
// Neither base provides libatomic.so.1, so hermes's libatomic1 is guarded on both.
func TestRenderDockerfileBuildsEveryAgent(t *testing.T) {
	origGet := docker.Get
	docker.Get = docker.RealClient
	t.Cleanup(func() { docker.Get = origGet })

	bases := []struct {
		name    string
		project []byte
	}{
		{"managed", nil},
		{"custom-fedora", []byte(
			"FROM fedora:latest\n" +
				"RUN dnf install -y curl && dnf clean all\n")},
	}

	for _, name := range agent.Names() {
		a, ok := agent.Lookup(name)
		if !ok {
			t.Fatalf("agent %q not registered", name)
		}
		version := integrationAgentVersion(t, a)
		for _, base := range bases {
			t.Run(name+"/"+base.name, func(t *testing.T) {
				tag := "agents-sandbox/it-all-" + sanitizeTag(name) + "-" + base.name
				t.Cleanup(func() { removeTestImage(tag) })

				buildDockerfileTag(
					t,
					a,
					renderBytesVersion(t, a, version, base.project, dockerOff),
					version,
					false,
					tag,
				)

				if got := imageAgentVersion(t, tag, a); got != version {
					t.Errorf("%s on %s: built image reports agent version %q, want %q", name, base.name, got, version)
				}
			})
		}
	}
}

// TestRenderDockerfileMissingGuestToolFailsBuild is the expected-failure guard
// for the in-VM runtime tool check: a custom base lacking a required tool must
// fail the build naming the tool, not build successfully and then fail at the
// daemon health probe.
func TestRenderDockerfileMissingGuestToolFailsBuild(t *testing.T) {
	origGet := docker.Get
	docker.Get = docker.RealClient
	t.Cleanup(func() { docker.Get = origGet })

	a, ok := agent.Lookup("opencode")
	if !ok {
		t.Fatal("opencode agent not registered")
	}
	version := integrationAgentVersion(t, a)

	ctx := context.Background()
	if _, err := docker.Get().Ping(ctx, client.PingOptions{}); err != nil {
		t.Skipf("docker daemon not available: %v", err)
	}

	// debian:trixie-slim ships no curl, so the runtime tool check must stop the build.
	tag := "agents-sandbox/it-missing-guest-tool"
	t.Cleanup(func() { removeTestImage(tag) })

	var lines []string
	buildErr := buildImage(
		ctx, a,
		renderBytesVersion(t, a, version, []byte("FROM debian:trixie-slim\n"), dockerOff),
		tag, false, version, "", "", false, imageIdentity{},
		func(s string) { lines = append(lines, s) },
	)
	if buildErr == nil {
		t.Fatal("the build must fail when the base lacks a required in-VM tool")
	}
	if out := strings.Join(lines, "\n"); !strings.Contains(out, "agent runtime tool missing: curl") {
		t.Errorf("the failed build must name the missing tool; output:\n%s", out)
	}
}

// TestRenderDockerfileNodeAgentNodeRuns is the regression guard for #132: a
// node-dependent agent installed onto the managed base must leave a working
// node/npm. The managed base does not install libatomic1, so a node tarball with
// a hard DT_NEEDED on libatomic.so.1 builds (for a native agent like opencode)
// but cannot start, and the npm agent's install RUN then fails the whole build.
// The pinned node must therefore be a release without that dependency; here we
// build pi (the only agent that needs node, an npm-installed node bundle) on the
// managed base and run node/npm in the resulting image.
//
// TestRenderDockerfileBuilds only builds opencode, a native Bun binary that does
// not exercise node at all, which is why it did not catch #132.
func TestRenderDockerfileNodeAgentNodeRuns(t *testing.T) {
	origGet := docker.Get
	docker.Get = docker.RealClient
	t.Cleanup(func() { docker.Get = origGet })

	a, ok := agent.Lookup("pi")
	if !ok {
		t.Fatal("pi agent not registered")
	}
	version := integrationAgentVersion(t, a)

	tag := "agents-sandbox/it-npm-agent-node"
	t.Cleanup(func() { removeTestImage(tag) })

	buildDockerfileTag(t, a, renderBytes(t, a, nil, dockerOff), version, false, tag)

	for _, command := range []string{"node --version", "npm --version"} {
		out, err := exec.Command("docker", "run", "--rm", tag, "bash", "-lc", command).CombinedOutput()
		if err != nil {
			t.Errorf("%q failed in the built image: %v\n%s", command, err, out)
		}
	}
}

// TestRenderDockerfileToolLayersStayCached is the regression guard for the
// per-tool composition: mutating only the user body must not invalidate the
// node, agent, or docker install layers. It builds once, then rebuilds with a
// changed body and asserts the tool-stage RUN steps are cache hits. It uses pi,
// the one agent whose render includes all three tool stages (node + agent +
// docker).
func TestRenderDockerfileToolLayersStayCached(t *testing.T) {
	origGet := docker.Get
	docker.Get = docker.RealClient
	t.Cleanup(func() { docker.Get = origGet })

	a, ok := agent.Lookup("pi")
	if !ok {
		t.Fatal("pi agent not registered")
	}
	version := integrationAgentVersion(t, a)

	base := []byte(
		"FROM fedora:latest\n" +
			"RUN dnf install -y curl tar iptables git procps-ng xz && dnf clean all\n" +
			"RUN echo body-v1\n")
	changed := []byte(
		"FROM fedora:latest\n" +
			"RUN dnf install -y curl tar iptables git procps-ng xz && dnf clean all\n" +
			"RUN echo body-v2\n")

	tag := "agents-sandbox/it-cache"
	t.Cleanup(func() { removeTestImage(tag) })

	// Prime the cache with the first body.
	buildDockerfileTag(t, a, renderBytesVersion(t, a, version, base, dockerOn), version, true, tag)

	// Rebuild with a changed body, capturing the streamed build output.
	var lines []string
	buildDockerfileTagOutput(t, a, renderBytesVersion(t, a, version, changed, dockerOn), version, true, tag, &lines)

	for _, stage := range []string{nodeStageAlias, agentStageAlias, dockerStageAlias} {
		if !stageStepCached(lines, stage) {
			t.Errorf("expected stage %q RUN to be a cache hit after only the user body changed; output:\n%s",
				stage, strings.Join(lines, "\n"))
		}
	}
}

// TestRenderDockerfileAgentVersionRebuildsInstall is the regression guard for
// the stale-agent-layer bug: after a version bump the runner image must carry
// the new release, while the node and docker install layers stay cached. It uses
// pi, the one agent whose render includes all three tool stages (node + agent +
// docker).
//
// It asserts the outcome — the version baked into the built image — rather than
// the agent install RUN being a cache miss. The classic builder's intermediate
// layers persist across test runs and across image removal, so a version-bump
// build is a cache hit on any run after the first; a cache-miss assertion would
// therefore pass only on a cold builder cache. The outcome is stable across
// runs and still catches the regression: a render that fails to fold the version
// into the agent stage reuses the previous release's layer, so the image reports
// the old version.
func TestRenderDockerfileAgentVersionRebuildsInstall(t *testing.T) {
	origGet := docker.Get
	docker.Get = docker.RealClient
	t.Cleanup(func() { docker.Get = origGet })

	a, ok := agent.Lookup("pi")
	if !ok {
		t.Fatal("pi agent not registered")
	}

	project := []byte(
		"FROM fedora:latest\n" +
			"RUN dnf install -y curl tar iptables git procps-ng xz && dnf clean all\n")

	tag := "agents-sandbox/it-agent-version"
	t.Cleanup(func() {
		_, _ = docker.Get().
			ImageRemove(context.Background(), tag, client.ImageRemoveOptions{Force: true, PruneChildren: true})
	})

	// Prime the image with an older release, then rebuild with a newer one. Both
	// are real published pi releases and differ, so the rebuild has a version
	// change to fold in.
	oldVersion, newVersion := "1.0.0", "1.1.0"
	buildDockerfileTag(t, a, renderBytesVersion(t, a, oldVersion, project, dockerOn), oldVersion, true, tag)

	var lines []string
	buildDockerfileTagOutput(
		t, a, renderBytesVersion(t, a, newVersion, project, dockerOn), newVersion, true, tag, &lines,
	)

	if got := imageAgentVersion(t, tag, a); got != newVersion {
		t.Errorf("built image reports agent version %q, want %q: the version bump did not reach the image",
			got, newVersion)
	}
	for _, stage := range []string{nodeStageAlias, dockerStageAlias} {
		if !stageStepCached(lines, stage) {
			t.Errorf("stage %q must stay cached on an agent version bump; output:\n%s",
				stage, strings.Join(lines, "\n"))
		}
	}
}

// imageAgentVersion runs the agent's own version probe inside the built runner
// image and returns the parsed version, so a test can assert which release the
// image actually carries.
func imageAgentVersion(t *testing.T, tag string, a agent.Agent) string {
	t.Helper()
	provider, ok := agent.AsVersionProvider(a)
	if !ok {
		t.Fatalf("agent %s has no version provider", a.Name())
	}
	command := provider.VersionCmd()
	out, err := exec.Command("docker", "run", "--rm", tag, "bash", "-lc", command).CombinedOutput()
	if err != nil {
		t.Fatalf("%q failed in the built image: %v\n%s", command, err, out)
	}
	version, err := provider.ParseVersion(string(out))
	if err != nil {
		t.Fatalf("parse agent version from %q: %v\n%s", command, err, out)
	}
	return version
}

// stageInstallMarkers maps each tool stage to a substring unique to its install
// RUN, so the streamed build output can be scanned for that step. The classic
// builder (what buildImage uses) prints "Step N/M : <instruction>" followed by
// " ---> Using cache", with no stage label; BuildKit prints "#N [<stage> ...]"
// followed by "#N CACHED". Keying on the instruction text works for both.
var stageInstallMarkers = map[string]string{
	nodeStageAlias:   "nodejs.org/dist",
	agentStageAlias:  "npm install",
	dockerStageAlias: "download.docker.com",
}

// stageStepCached reports whether the install RUN step of the given stage was a
// cache hit in a streamed build, handling both BuildKit (#N CACHED) and classic
// (---> Using cache) output.
func stageStepCached(lines []string, stage string) bool {
	marker, ok := stageInstallMarkers[stage]
	if !ok {
		return false
	}
	for i, line := range lines {
		if !strings.Contains(line, marker) {
			continue
		}
		if len(lines) > i+1 && (strings.Contains(lines[i+1], "CACHED") || strings.Contains(lines[i+1], "Using cache")) {
			return true
		}
	}
	return false
}

// buildDockerfile builds the rendered Dockerfile through the production
// buildImage helper against the real docker daemon, failing the test if the
// build does not succeed. The built image is removed on test cleanup.
func buildDockerfile(t *testing.T, a agent.Agent, dockerfile []byte, agentVersion string, dockerEnabled bool) {
	t.Helper()
	tag := "agents-sandbox/it-" + sanitizeTag(t.Name())
	buildDockerfileTag(t, a, dockerfile, agentVersion, dockerEnabled, tag)
	t.Cleanup(func() { removeTestImage(tag) })
}

func buildDockerfileTag(
	t *testing.T,
	a agent.Agent,
	dockerfile []byte,
	agentVersion string,
	dockerEnabled bool,
	tag string,
) {
	t.Helper()
	buildDockerfileTagOutput(t, a, dockerfile, agentVersion, dockerEnabled, tag, nil)
}

func buildDockerfileTagOutput(
	t *testing.T,
	a agent.Agent,
	dockerfile []byte,
	agentVersion string,
	dockerEnabled bool,
	tag string,
	lines *[]string,
) {
	t.Helper()

	ctx := context.Background()
	if _, err := docker.Get().Ping(ctx, client.PingOptions{}); err != nil {
		t.Skipf("docker daemon not available: %v", err)
	}

	line := func(s string) {
		if lines != nil {
			*lines = append(*lines, s)
		}
	}
	if err := buildImage(
		ctx,
		a,
		dockerfile,
		tag,
		false,
		agentVersion,
		"",
		"",
		dockerEnabled,
		imageIdentity{},
		line,
	); err != nil {
		t.Fatalf("docker image build failed: %v", err)
	}
}

// TestRenderDockerfileLoginShellHasImagePath proves the profile.d merge fires
// in a real login shell: /etc/profile resets PATH, and the installed script
// re-adds the image's entries. Covers the managed base (its /opt/agents-sandbox
// is the entry the reset drops) and a project ENV PATH entry.
func TestRenderDockerfileLoginShellHasImagePath(t *testing.T) {
	origGet := docker.Get
	docker.Get = docker.RealClient
	t.Cleanup(func() { docker.Get = origGet })

	a, _ := agent.Lookup("opencode")
	version := integrationAgentVersion(t, a)
	project := []byte("FROM agents-sandbox/runner-base:latest\nENV PATH=\"/opt/my-tools:${PATH}\"\n")

	tag := "agents-sandbox/it-login-path"
	t.Cleanup(func() { removeTestImage(tag) })
	buildDockerfileTag(t, a, renderBytesVersion(t, a, version, project, dockerOff), version, false, tag)

	out, err := exec.Command("docker", "run", "--rm", tag, "bash", "-lc", `printf '%s' "$PATH"`).Output()
	if err != nil {
		t.Fatalf("docker run: %v", err)
	}
	loginPath := string(out)
	for _, want := range []string{"/opt/agents-sandbox/bin", "/opt/my-tools"} {
		if !strings.Contains(loginPath, want) {
			t.Errorf("login PATH %q missing %q", loginPath, want)
		}
	}
}

// TestRenderDockerfileTrustHelperRunsInImage proves the CA-trust helper lands in
// the built image as a runnable command, which is what a Java user needs to
// import the interception CA into a JDK trust store, and that its license lands
// beside it. The executable bit travels in the build context rather than in the
// Dockerfile, so nothing short of a real build and run shows that the installed
// file can be executed; asserting on the guest CA path also catches a vendored
// script that no longer matches our guest layout. Invoking the command directly
// (no shell) is what exercises the mode.
func TestRenderDockerfileTrustHelperRunsInImage(t *testing.T) {
	origGet := docker.Get
	docker.Get = docker.RealClient
	t.Cleanup(func() { docker.Get = origGet })

	a, ok := agent.Lookup("opencode")
	if !ok {
		t.Fatal("opencode agent not registered")
	}
	version := integrationAgentVersion(t, a)

	tag := "agents-sandbox/it-trust-helper"
	t.Cleanup(func() { removeTestImage(tag) })
	buildDockerfileTag(t, a, renderBytesVersion(t, a, version, nil, dockerOff), version, false, tag)

	out, err := exec.Command("docker", "run", "--rm", tag, "msb-trust", "java", "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("msb-trust java --help failed in the built image: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "/.msb/tls/ca.pem") {
		t.Errorf("helper output does not name the guest interception CA:\n%s", out)
	}

	license, err := exec.Command("docker", "run", "--rm", tag, "cat", trustHelperLicenseDest).CombinedOutput()
	if err != nil {
		t.Fatalf("reading the installed trust helper license failed: %v\n%s", err, license)
	}
	if !strings.Contains(string(license), "Apache License") {
		t.Errorf("installed license at %s is not the vendored Apache-2.0 text:\n%s", trustHelperLicenseDest, license)
	}
}

// removeTestImage removes a test image by tag while preserving the shared build
// cache. PruneChildren must stay false: true selects Docker's "prune" default,
// which also deletes the untagged parent images the classic builder uses as its
// layer cache, forcing every later build (and every later run) to re-run the
// node/agent/docker installs. The leftover image is dangling and harmless.
func removeTestImage(tag string) {
	_, _ = docker.Get().
		ImageRemove(context.Background(), tag, client.ImageRemoveOptions{Force: true, PruneChildren: false})
}

// sanitizeTag renders a test name into a docker-tag-safe lowercase string.
func sanitizeTag(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r - 'A' + 'a')
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}
