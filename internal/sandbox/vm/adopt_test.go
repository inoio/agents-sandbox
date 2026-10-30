package vm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/git"
	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

// The adoption tests use two fixed slugs so every path in the notice and every
// assertion is spelled out literally: widget-aaa is the legacy origin-derived
// identity, widget-bbb the checkout-scoped one.
const (
	adoptRepoSlug     = "widget-aaa"
	adoptCheckoutSlug = "widget-bbb"
	adoptAgentName    = "opencode"
	// adoptLegacyVolume is the home-volume name recorded in the legacy state,
	// embedding the legacy repo slug because the name rides along on migration.
	adoptLegacyVolume = "agents-sandbox-home-widget-aaa-opencode-1"
)

func adoptIdentity() git.Identity {
	return git.Identity{
		RepoSlug:     adoptRepoSlug,
		CheckoutSlug: adoptCheckoutSlug,
		BindRoot:     "/repo",
		CheckoutRoot: "/repo",
	}
}

func adoptRepoKey() state.Key     { return state.Key{Slug: adoptRepoSlug, Agent: adoptAgentName} }
func adoptCheckoutKey() state.Key { return state.Key{Slug: adoptCheckoutSlug, Agent: adoptAgentName} }

func adoptLegacyVMName() string { return projectVMName(adoptRepoKey()) }

// seedLegacyState writes the legacy origin-keyed state file that triggers
// adoption, and returns the path it wrote so a test can assert it survives.
func seedLegacyState(t *testing.T) string {
	t.Helper()
	if err := seedHomeState(adoptRepoKey(), state.HomeState{HomeVolume: adoptLegacyVolume}); err != nil {
		t.Fatalf("seed legacy state: %v", err)
	}
	return filepath.Join(state.KeyDir(adoptRepoKey()), "state.yaml")
}

// statePresent reports whether the key has a readable state file, failing the
// test on any read error other than a missing file.
func statePresent(t *testing.T, k state.Key) bool {
	t.Helper()
	_, err := state.ReadState(k)
	switch {
	case err == nil:
		return true
	case errors.Is(err, state.ErrStateNotFound):
		return false
	default:
		t.Fatalf("ReadState(%v): %v", k, err)
		return false
	}
}

// TestAdoptLegacyIdentityMovesStateAndDestroysVM covers the happy path: the
// legacy VM is destroyed and the legacy state directory merges onto the
// checkout slug, so the caller continues under the checkout identity.
func TestAdoptLegacyIdentityMovesStateAndDestroysVM(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	seedLegacyState(t)

	handle := &msb.MockSandboxHandle{Name_: adoptLegacyVMName()}
	var requested string
	client := &msb.MockMsbClient{GetSandboxFn: func(_ context.Context, name string) (msb.SandboxHandle, error) {
		requested = name
		return handle, nil
	}}
	ui := termio.NewTestMock(t)

	got, err := AdoptLegacyIdentity(context.Background(), client, adoptIdentity(), adoptAgentName, true, &ui)
	if err != nil {
		t.Fatalf("AdoptLegacyIdentity: %v", err)
	}

	if requested != adoptLegacyVMName() {
		t.Errorf("GetSandbox name = %q, want %q", requested, adoptLegacyVMName())
	}
	if !handle.Destroyed {
		t.Error("legacy VM was not destroyed")
	}
	var sawDestroyed bool
	for _, call := range ui.VerboseCalls {
		if strings.Contains(call, "destroyed legacy project VM") {
			sawDestroyed = true
		}
	}
	if !sawDestroyed {
		t.Errorf("expected the destroyed-VM verbose note; got %v", ui.VerboseCalls)
	}
	if !statePresent(t, adoptCheckoutKey()) {
		t.Error("no state file under the checkout slug after adoption")
	}
	if _, statErr := os.Stat(state.SlugDir(adoptRepoSlug)); !os.IsNotExist(statErr) {
		t.Errorf("legacy slug dir still present (stat error = %v)", statErr)
	}
	want := IdentityAdoption{Slug: adoptCheckoutSlug, Pending: true, Applied: true}
	if got != want {
		t.Errorf("adoption = %+v, want %+v", got, want)
	}
}

// TestAdoptLegacyIdentityNoticeNamesEveryPath pins the notice's content: it is
// the only place the user learns where their state went, so every moved or
// recreated path has to appear in the block.
func TestAdoptLegacyIdentityNoticeNamesEveryPath(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	seedLegacyState(t)

	handle := &msb.MockSandboxHandle{Name_: adoptLegacyVMName()}
	client := &msb.MockMsbClient{GetSandboxFn: func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return handle, nil
	}}
	ui := termio.NewTestMock(t)

	if _, err := AdoptLegacyIdentity(
		context.Background(),
		client,
		adoptIdentity(),
		adoptAgentName,
		true,
		&ui,
	); err != nil {
		t.Fatalf("AdoptLegacyIdentity: %v", err)
	}

	notice := strings.Join(ui.WarnCalls, "\n")
	for _, want := range []string{
		"Migrating this checkout to its own project identity",
		state.SlugDir(adoptRepoSlug),
		state.SlugDir(adoptCheckoutSlug),
		adoptLegacyVMName(),
		projectVMName(adoptCheckoutKey()),
		adoptLegacyVolume,
		naming.RunnerImageRef(adoptRepoSlug, adoptAgentName),
		naming.RunnerImageRef(adoptCheckoutSlug, adoptAgentName),
	} {
		if !strings.Contains(notice, want) {
			t.Errorf("notice missing %q:\n%s", want, notice)
		}
	}
}

// TestAdoptLegacyIdentityNoticeWithoutHomeVolume pins the placeholder the notice
// renders when the legacy state records no home volume, so the row never shows
// an empty value.
func TestAdoptLegacyIdentityNoticeWithoutHomeVolume(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if err := seedHomeState(adoptRepoKey(), state.HomeState{}); err != nil {
		t.Fatalf("seed legacy state: %v", err)
	}

	client := &msb.MockMsbClient{GetSandboxFn: func(_ context.Context, name string) (msb.SandboxHandle, error) {
		t.Errorf("GetSandbox(%q) called during a dry run", name)
		return nil, errors.New("GetSandbox must not be called")
	}}
	ui := termio.NewTestMock(t)

	got, err := AdoptLegacyIdentity(context.Background(), client, adoptIdentity(), adoptAgentName, false, &ui)
	if err != nil {
		t.Fatalf("AdoptLegacyIdentity: %v", err)
	}

	if notice := strings.Join(ui.WarnCalls, "\n"); !strings.Contains(notice, "(none recorded)") {
		t.Errorf("notice missing the empty home-volume placeholder:\n%s", notice)
	}
	if !got.Pending {
		t.Errorf("adoption = %+v, want a pending migration", got)
	}
}

// TestAdoptLegacyIdentityNoLegacyState covers a project that never had the
// legacy identity: no legacy state file and no surviving legacy VM, so nothing is
// destroyed, moved or printed.
func TestAdoptLegacyIdentityNoLegacyState(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	client := &msb.MockMsbClient{GetSandboxFn: func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "no legacy VM"}
	}}
	ui := termio.NewTestMock(t)

	got, err := AdoptLegacyIdentity(context.Background(), client, adoptIdentity(), adoptAgentName, true, &ui)
	if err != nil {
		t.Fatalf("AdoptLegacyIdentity: %v", err)
	}

	if got != (IdentityAdoption{Slug: adoptCheckoutSlug}) {
		t.Errorf("adoption = %+v, want the checkout slug with nothing pending", got)
	}
	if len(ui.WarnCalls) != 0 || len(ui.InfoCalls) != 0 || len(ui.VerboseCalls) != 0 ||
		len(ui.OutCalls) != 0 || len(ui.PrintCalls) != 0 {
		t.Errorf("expected no output, got warn=%v info=%v verbose=%v out=%v print=%v",
			ui.WarnCalls, ui.InfoCalls, ui.VerboseCalls, ui.OutCalls, ui.PrintCalls)
	}
}

// TestAdoptLegacyIdentityAlreadyAtCheckoutSlug covers the re-run case: a
// checkout-keyed state file makes the trigger false even while the legacy
// directory still exists, so the legacy state is deliberately left alone.
func TestAdoptLegacyIdentityAlreadyAtCheckoutSlug(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacyStateFile := seedLegacyState(t)
	if err := seedHomeState(adoptCheckoutKey(), state.HomeState{HomeVolume: "home-bbb"}); err != nil {
		t.Fatalf("seed checkout state: %v", err)
	}

	client := &msb.MockMsbClient{GetSandboxFn: func(_ context.Context, name string) (msb.SandboxHandle, error) {
		t.Errorf("GetSandbox(%q) called for an already-adopted project", name)
		return nil, errors.New("GetSandbox must not be called")
	}}
	ui := termio.NewTestMock(t)

	got, err := AdoptLegacyIdentity(context.Background(), client, adoptIdentity(), adoptAgentName, true, &ui)
	if err != nil {
		t.Fatalf("AdoptLegacyIdentity: %v", err)
	}

	if got != (IdentityAdoption{Slug: adoptCheckoutSlug}) {
		t.Errorf("adoption = %+v, want a no-op at the checkout slug", got)
	}
	if _, statErr := os.Stat(legacyStateFile); statErr != nil {
		t.Errorf("legacy state file moved by a no-op adoption: %v", statErr)
	}
	if len(ui.WarnCalls) != 0 {
		t.Errorf("expected no notice, got %v", ui.WarnCalls)
	}
}

// TestAdoptLegacyIdentityUnparsableLegacyState verifies the fail-closed
// predicate: an unreadable legacy document aborts adoption before anything is
// destroyed or moved, because the caller cannot know what it would be adopting.
func TestAdoptLegacyIdentityUnparsableLegacyState(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacyDir := filepath.Join(state.SlugDir(adoptRepoSlug), adoptAgentName)
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatalf("create legacy state dir: %v", err)
	}
	testutil.WriteFile(t, legacyDir, "state.yaml", `": [`)

	handle := &msb.MockSandboxHandle{Name_: adoptLegacyVMName()}
	client := &msb.MockMsbClient{GetSandboxFn: func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return handle, nil
	}}
	ui := termio.NewTestMock(t)

	if _, err := AdoptLegacyIdentity(
		context.Background(),
		client,
		adoptIdentity(),
		adoptAgentName,
		true,
		&ui,
	); err == nil {
		t.Fatal("expected an unparsable legacy state to fail adoption")
	} else if !errors.Is(err, state.ErrStateUnparsable) {
		t.Errorf("error = %v, want it to preserve the unparsable-state cause", err)
	}

	if handle.Destroyed {
		t.Error("legacy VM destroyed despite an unparsable legacy state")
	}
	if statePresent(t, adoptCheckoutKey()) {
		t.Error("state moved despite an unparsable legacy state")
	}
	if _, statErr := os.Stat(filepath.Join(legacyDir, "state.yaml")); statErr != nil {
		t.Errorf("legacy state file removed: %v", statErr)
	}
}

// TestAdoptLegacyIdentityApplyFalseIsReadOnly covers the dry run: the notice is
// printed under the "would" headline, nothing is touched, and the caller is told
// to keep resolving against the legacy slug.
func TestAdoptLegacyIdentityApplyFalseIsReadOnly(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacyStateFile := seedLegacyState(t)

	client := &msb.MockMsbClient{GetSandboxFn: func(_ context.Context, name string) (msb.SandboxHandle, error) {
		t.Errorf("GetSandbox(%q) called during a dry run", name)
		return nil, errors.New("GetSandbox must not be called")
	}}
	ui := termio.NewTestMock(t)

	got, err := AdoptLegacyIdentity(context.Background(), client, adoptIdentity(), adoptAgentName, false, &ui)
	if err != nil {
		t.Fatalf("AdoptLegacyIdentity: %v", err)
	}

	notice := strings.Join(ui.WarnCalls, "\n")
	for _, want := range []string{
		"Would migrate this checkout to its own project identity",
		state.SlugDir(adoptCheckoutSlug),
		adoptLegacyVolume,
	} {
		if !strings.Contains(notice, want) {
			t.Errorf("dry-run notice missing %q:\n%s", want, notice)
		}
	}
	if statePresent(t, adoptCheckoutKey()) {
		t.Error("state moved during a dry run")
	}
	if _, statErr := os.Stat(legacyStateFile); statErr != nil {
		t.Errorf("legacy state removed during a dry run: %v", statErr)
	}
	want := IdentityAdoption{Slug: adoptRepoSlug, Pending: true, Applied: false}
	if got != want {
		t.Errorf("adoption = %+v, want %+v", got, want)
	}
}

// TestAdoptLegacyIdentityLegacyVMAlreadyGone verifies that an absent legacy VM
// does not block adoption: the state move is the part that matters.
func TestAdoptLegacyIdentityLegacyVMAlreadyGone(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	seedLegacyState(t)

	client := &msb.MockMsbClient{GetSandboxFn: func(_ context.Context, name string) (msb.SandboxHandle, error) {
		return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: name}
	}}
	ui := termio.NewTestMock(t)

	got, err := AdoptLegacyIdentity(context.Background(), client, adoptIdentity(), adoptAgentName, true, &ui)
	if err != nil {
		t.Fatalf("AdoptLegacyIdentity: %v", err)
	}

	if !statePresent(t, adoptCheckoutKey()) {
		t.Error("state not moved although the legacy VM was already gone")
	}
	if got.Applied != true {
		t.Errorf("adoption = %+v, want it applied", got)
	}
	var sawAbsentLegacyVM bool
	for _, call := range ui.VerboseCalls {
		if strings.Contains(call, "legacy project VM") && strings.Contains(call, "is already gone") {
			sawAbsentLegacyVM = true
		}
	}
	if !sawAbsentLegacyVM {
		t.Errorf("expected the absent-legacy-VM verbose note; got %v", ui.VerboseCalls)
	}
}

// TestAdoptLegacyIdentityLegacyVMReplacedFailsClosed verifies the
// identity-aware destroy: the name now belongs to a sandbox this run did not
// create, so the VM is left alone and the state is not moved.
func TestAdoptLegacyIdentityLegacyVMReplacedFailsClosed(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	seedLegacyState(t)

	handle := &msb.MockSandboxHandle{
		Name_:      "widget-aaa--renamed-sandbox",
		DestroyErr: &msbSdk.Error{Kind: msbSdk.ErrSandboxReplaced, Message: "replaced"},
	}
	client := &msb.MockMsbClient{GetSandboxFn: func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return handle, nil
	}}
	ui := termio.NewTestMock(t)

	_, err := AdoptLegacyIdentity(context.Background(), client, adoptIdentity(), adoptAgentName, true, &ui)
	if err == nil {
		t.Fatal("expected a replaced legacy VM to fail adoption")
	}
	if !msb.IsReplaced(err) {
		t.Errorf("error = %v, want it to wrap ErrSandboxReplaced", err)
	}
	if !strings.Contains(err.Error(), "rerun to finish migrating this checkout") {
		t.Errorf("error = %v, want the adoption action clause from destroyProjectVMHandle", err)
	}
	if !strings.Contains(err.Error(), handle.Name()) {
		t.Errorf("error = %v, want it to name the resolved handle %q", err, handle.Name())
	}
	if statePresent(t, adoptCheckoutKey()) {
		t.Error("state moved despite a replaced legacy VM")
	}
	if !statePresent(t, adoptRepoKey()) {
		t.Error("legacy state removed despite a replaced legacy VM")
	}
}

// TestAdoptLegacyIdentityStrandedStateFileFailsLoudly covers the mandatory
// post-move check. A dangling symlink at the destination agent directory makes
// the merge refuse to overwrite it and report a skip, so the legacy state file
// never arrives. Continuing would run under the checkout identity with no state
// — a fresh home volume and the agent's auth silently gone — so adoption must
// fail loudly, naming what is half-done and what to clear to finish it.
func TestAdoptLegacyIdentityStrandedStateFileFailsLoudly(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacyStateFile := seedLegacyState(t)

	checkoutSlugDir := state.SlugDir(adoptCheckoutSlug)
	if err := os.MkdirAll(checkoutSlugDir, 0o700); err != nil {
		t.Fatalf("create checkout state dir: %v", err)
	}
	if err := os.Symlink(
		filepath.Join(t.TempDir(), "nowhere"),
		filepath.Join(checkoutSlugDir, adoptAgentName),
	); err != nil {
		t.Fatalf("create blocking symlink: %v", err)
	}

	handle := &msb.MockSandboxHandle{Name_: adoptLegacyVMName()}
	client := &msb.MockMsbClient{GetSandboxFn: func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return handle, nil
	}}
	ui := termio.NewTestMock(t)

	_, err := AdoptLegacyIdentity(context.Background(), client, adoptIdentity(), adoptAgentName, true, &ui)
	if err == nil {
		t.Fatal("expected a stranded state file to fail adoption")
	}
	for _, want := range []string{
		"did not complete",
		legacyStateFile,
		"skipped [" + adoptAgentName + "]",
		adoptLegacyVMName() + " is already gone",
		checkoutSlugDir,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q:\n%v", want, err)
		}
	}
	if !errors.Is(err, state.ErrStateNotFound) {
		t.Errorf("error = %v, want it to preserve the missing-state cause", err)
	}
	var sawSkipWarning bool
	for _, call := range ui.WarnCalls {
		if strings.Contains(call, "left "+adoptAgentName+" in the legacy state directory") {
			sawSkipWarning = true
		}
	}
	if !sawSkipWarning {
		t.Errorf("expected the skipped-entry warning; got %v", ui.WarnCalls)
	}
	if _, statErr := os.Stat(legacyStateFile); statErr != nil {
		t.Errorf("legacy state file gone after a loud failure: %v", statErr)
	}
}

// TestAdoptLegacyIdentityUnparsableCheckoutStateSkipsMigration pins the
// skip-don't-block predicate on the checkout side: a corrupt checkout-keyed
// document must not abort the run before the recovery path (RecoverHomeState) can
// repair it, so adoption reports nothing pending and leaves the legacy VM and
// state untouched.
func TestAdoptLegacyIdentityUnparsableCheckoutStateSkipsMigration(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	legacyStateFile := seedLegacyState(t)
	checkoutDir := state.KeyDir(adoptCheckoutKey())
	if err := os.MkdirAll(checkoutDir, 0o700); err != nil {
		t.Fatalf("create checkout state dir: %v", err)
	}
	testutil.WriteFile(t, checkoutDir, "state.yaml", `": [`)

	client := &msb.MockMsbClient{GetSandboxFn: func(_ context.Context, name string) (msb.SandboxHandle, error) {
		t.Errorf("GetSandbox(%q) called despite an unreadable checkout state", name)
		return nil, errors.New("GetSandbox must not be called")
	}}
	ui := termio.NewTestMock(t)

	adoption, err := AdoptLegacyIdentity(context.Background(), client, adoptIdentity(), adoptAgentName, true, &ui)
	if err != nil {
		t.Fatalf("a corrupt checkout state must not abort the run: %v", err)
	}
	if adoption.Pending || adoption.Applied {
		t.Errorf("adoption = %+v, want nothing pending", adoption)
	}
	if _, statErr := os.Stat(legacyStateFile); statErr != nil {
		t.Errorf("legacy state file gone after a skipped migration: %v", statErr)
	}
}

// TestAdoptLegacyIdentityNonRepoCorruptStateDoesNotBlock covers the empty-legacy
// case: outside a repository there is no legacy identity to adopt, so a corrupt
// checkout state must not block the run's own recovery path.
func TestAdoptLegacyIdentityNonRepoCorruptStateDoesNotBlock(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	// Both slugs equal for a non-repo directory, so legacySlugs is empty.
	identity := git.Identity{RepoSlug: "workspace-ccc", CheckoutSlug: "workspace-ccc"}
	key := state.Key{Slug: "workspace-ccc", Agent: adoptAgentName}
	if err := os.MkdirAll(state.KeyDir(key), 0o700); err != nil {
		t.Fatalf("create checkout state dir: %v", err)
	}
	testutil.WriteFile(t, state.KeyDir(key), "state.yaml", `": [`)

	client := &msb.MockMsbClient{GetSandboxFn: func(_ context.Context, name string) (msb.SandboxHandle, error) {
		t.Errorf("GetSandbox(%q) called with no legacy identity", name)
		return nil, errors.New("GetSandbox must not be called")
	}}
	ui := termio.NewTestMock(t)

	adoption, err := AdoptLegacyIdentity(context.Background(), client, identity, adoptAgentName, true, &ui)
	if err != nil {
		t.Fatalf("a corrupt state outside a repo must not block the run: %v", err)
	}
	if adoption.Pending || adoption.Applied {
		t.Errorf("adoption = %+v, want nothing pending", adoption)
	}
}

// TestAdoptLegacyIdentityStateMoveFailureFailsLoudly covers the state-move
// failure: the trigger sees no checkout state file through a dangling
// destination symlink, but the merge then cannot create the destination, so
// adoption fails loudly instead of continuing with no state.
func TestAdoptLegacyIdentityStateMoveFailureFailsLoudly(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	seedLegacyState(t)
	if err := os.Symlink(
		filepath.Join(t.TempDir(), "nowhere"),
		state.SlugDir(adoptCheckoutSlug),
	); err != nil {
		t.Fatalf("create dangling destination symlink: %v", err)
	}

	handle := &msb.MockSandboxHandle{Name_: adoptLegacyVMName()}
	client := &msb.MockMsbClient{GetSandboxFn: func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return handle, nil
	}}
	ui := termio.NewTestMock(t)

	_, err := AdoptLegacyIdentity(context.Background(), client, adoptIdentity(), adoptAgentName, true, &ui)
	if err == nil {
		t.Fatal("expected a failed state move to fail adoption")
	}
	if !strings.Contains(err.Error(), "move project state to") {
		t.Errorf("error = %v, want the state-move failure", err)
	}
	if !errors.As(err, new(*os.PathError)) {
		t.Errorf("error = %v, want it to preserve the filesystem cause", err)
	}
	if statePresent(t, adoptCheckoutKey()) {
		t.Error("state present after a failed move")
	}
}

// TestAdoptLegacyIdentityGetSandboxErrorAborts covers a non-not-found failure
// resolving the legacy VM: adoption must abort rather than move state and leave
// an unknown VM running against the shared home volume.
func TestAdoptLegacyIdentityGetSandboxErrorAborts(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	seedLegacyState(t)

	client := &msb.MockMsbClient{GetSandboxFn: func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return nil, errLegacyVMBoom
	}}
	ui := termio.NewTestMock(t)

	_, err := AdoptLegacyIdentity(context.Background(), client, adoptIdentity(), adoptAgentName, true, &ui)
	if err == nil {
		t.Fatal("expected a GetSandbox failure to abort adoption")
	}
	if !strings.Contains(err.Error(), "check legacy project VM") {
		t.Errorf("error = %v, want the legacy-VM check failure", err)
	}
	if !errors.Is(err, errLegacyVMBoom) {
		t.Errorf("error = %v, want it to wrap the GetSandbox cause", err)
	}
	if statePresent(t, adoptCheckoutKey()) {
		t.Error("state moved despite a failed legacy-VM check")
	}
}

// TestAdoptLegacyIdentityLegacyVMAlreadyGoneOnDestroy covers the identity-safe
// teardown's already-gone outcome: the name vanished between the check and the
// destroy, so the move continues and the caller is told.
func TestAdoptLegacyIdentityLegacyVMAlreadyGoneOnDestroy(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	seedLegacyState(t)

	handle := &msb.MockSandboxHandle{
		Name_:      adoptLegacyVMName(),
		DestroyErr: &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "gone"},
	}
	client := &msb.MockMsbClient{GetSandboxFn: func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return handle, nil
	}}
	ui := termio.NewTestMock(t)

	got, err := AdoptLegacyIdentity(context.Background(), client, adoptIdentity(), adoptAgentName, true, &ui)
	if err != nil {
		t.Fatalf("AdoptLegacyIdentity: %v", err)
	}
	if !statePresent(t, adoptCheckoutKey()) {
		t.Error("state not moved although the legacy VM vanished before the destroy")
	}
	if !got.Applied {
		t.Errorf("adoption = %+v, want it applied", got)
	}
	var sawNote bool
	for _, call := range ui.VerboseCalls {
		if strings.Contains(call, "was already gone; continuing") {
			sawNote = true
		}
	}
	if !sawNote {
		t.Errorf("expected the already-gone verbose note; got %v", ui.VerboseCalls)
	}
}

// TestAdoptLegacyIdentityDestroysEveryMovedAgentsVM covers the multi-agent case:
// moving the slug directory carries every agent's state, so every moved agent's
// legacy VM must be destroyed — not only the one the command named — or the
// left-behind sibling keeps writing the shared home volume under its old name.
func TestAdoptLegacyIdentityDestroysEveryMovedAgentsVM(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	seedLegacyState(t)
	piKey := state.Key{Slug: adoptRepoSlug, Agent: "pi"}
	if err := seedHomeState(piKey, state.HomeState{HomeVolume: "home-pi"}); err != nil {
		t.Fatalf("seed pi state: %v", err)
	}

	handles := map[string]*msb.MockSandboxHandle{}
	client := &msb.MockMsbClient{GetSandboxFn: func(_ context.Context, name string) (msb.SandboxHandle, error) {
		handle := &msb.MockSandboxHandle{Name_: name}
		handles[name] = handle
		return handle, nil
	}}
	ui := termio.NewTestMock(t)

	got, err := AdoptLegacyIdentity(context.Background(), client, adoptIdentity(), adoptAgentName, true, &ui)
	if err != nil {
		t.Fatalf("AdoptLegacyIdentity: %v", err)
	}

	for _, name := range []string{projectVMName(adoptRepoKey()), projectVMName(piKey)} {
		handle, ok := handles[name]
		if !ok {
			t.Errorf("GetSandbox never called for %q", name)
			continue
		}
		if !handle.Destroyed {
			t.Errorf("legacy VM %q was not destroyed", name)
		}
	}
	if !statePresent(t, state.Key{Slug: adoptCheckoutSlug, Agent: "pi"}) {
		t.Error("pi state was not moved to the checkout slug")
	}
	if !got.Applied {
		t.Errorf("adoption = %+v, want it applied", got)
	}
}

// TestAdoptLegacyIdentityLosingTheLockRaceContinues covers two runs racing for the
// shared legacy identity: the loser re-checks the trigger under the root lock,
// sees the state already moved, and continues under the checkout identity instead
// of tripping the incomplete-migration failure on a source that is already gone.
func TestAdoptLegacyIdentityLosingTheLockRaceContinues(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	seedLegacyState(t)

	adoptLockHook = func() {
		if err := seedHomeState(adoptCheckoutKey(), state.HomeState{HomeVolume: "home-bbb"}); err != nil {
			t.Errorf("seed checkout state inside the lock hook: %v", err)
		}
	}
	t.Cleanup(func() { adoptLockHook = nil })

	client := &msb.MockMsbClient{GetSandboxFn: func(_ context.Context, name string) (msb.SandboxHandle, error) {
		t.Errorf("GetSandbox(%q) called although another adopter won the lock", name)
		return nil, errors.New("GetSandbox must not be called")
	}}
	ui := termio.NewTestMock(t)

	got, err := AdoptLegacyIdentity(context.Background(), client, adoptIdentity(), adoptAgentName, true, &ui)
	if err != nil {
		t.Fatalf("AdoptLegacyIdentity: %v", err)
	}
	want := IdentityAdoption{Slug: adoptCheckoutSlug}
	if got != want {
		t.Errorf("adoption = %+v, want %+v", got, want)
	}
}

// TestAdoptLegacyIdentityAdoptsBareRootLegacySlug covers the bare root's second
// legacy identity: state under the pre-split path-derived slug is moved onto the
// shared checkout slug and its legacy VM is destroyed, even though the
// origin-derived repo slug has no state at all.
func TestAdoptLegacyIdentityAdoptsBareRootLegacySlug(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	const legacySlug = "workspace-aaa"

	identity := adoptIdentity()
	identity.LegacyRepoSlugs = []string{legacySlug}
	legacyKey := state.Key{Slug: legacySlug, Agent: adoptAgentName}
	if err := seedHomeState(legacyKey, state.HomeState{HomeVolume: "home-legacy"}); err != nil {
		t.Fatalf("seed bare-root legacy state: %v", err)
	}

	handles := map[string]*msb.MockSandboxHandle{}
	client := &msb.MockMsbClient{GetSandboxFn: func(_ context.Context, name string) (msb.SandboxHandle, error) {
		handle := &msb.MockSandboxHandle{Name_: name}
		handles[name] = handle
		return handle, nil
	}}
	ui := termio.NewTestMock(t)

	got, err := AdoptLegacyIdentity(context.Background(), client, identity, adoptAgentName, true, &ui)
	if err != nil {
		t.Fatalf("AdoptLegacyIdentity: %v", err)
	}
	if !statePresent(t, adoptCheckoutKey()) {
		t.Error("bare-root legacy state was not moved to the checkout slug")
	}
	if handle := handles[projectVMName(legacyKey)]; handle == nil || !handle.Destroyed {
		t.Errorf("bare-root legacy VM was not destroyed (handles=%v)", handles)
	}
	want := IdentityAdoption{Slug: adoptCheckoutSlug, Pending: true, Applied: true}
	if got != want {
		t.Errorf("adoption = %+v, want %+v", got, want)
	}
}

// TestMigrateLegacyConfigDirsMovesBareRootConfig pins the config half of the
// bare-root adoption: the pre-split per-slug config directory is renamed onto the
// repo slug the launcher reads now, so the user keeps their settings.
func TestMigrateLegacyConfigDirsMovesBareRootConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	configpaths.WithRealConfigPaths(t)
	paths := configpaths.Get()

	from := paths.UserSlugConfigDir("workspace-aaa")
	to := paths.UserSlugConfigDir("agents-sandbox-bbb")
	if err := os.MkdirAll(from, 0o700); err != nil {
		t.Fatalf("mkdir legacy config dir: %v", err)
	}
	testutil.WriteFile(t, from, "config.yaml", "x: 1\n")

	ui := termio.NewTestMock(t)
	identity := git.Identity{RepoSlug: "agents-sandbox-bbb", LegacyRepoSlugs: []string{"workspace-aaa"}}

	if err := migrateLegacyConfigDirs(identity, &ui); err != nil {
		t.Fatalf("migrateLegacyConfigDirs: %v", err)
	}
	if _, err := os.Stat(filepath.Join(to, "config.yaml")); err != nil {
		t.Errorf("config not moved to the repo slug dir: %v", err)
	}
	if _, err := os.Stat(from); !os.IsNotExist(err) {
		t.Errorf("legacy config dir still present (stat err = %v)", err)
	}
	var sawMoved bool
	for _, call := range ui.VerboseCalls {
		if strings.Contains(call, "moved legacy config dir") {
			sawMoved = true
		}
	}
	if !sawMoved {
		t.Errorf("expected the moved-config-dir verbose note; got %v", ui.VerboseCalls)
	}
}

// TestMigrateLegacyConfigDirsKeepsExistingTarget pins the never-overwrite rule:
// when the repo-slug config dir already exists, the legacy copy is left in place
// rather than clobbering the config the user is using.
func TestMigrateLegacyConfigDirsKeepsExistingTarget(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	configpaths.WithRealConfigPaths(t)
	paths := configpaths.Get()

	from := paths.UserSlugConfigDir("workspace-aaa")
	to := paths.UserSlugConfigDir("agents-sandbox-bbb")
	if err := os.MkdirAll(from, 0o700); err != nil {
		t.Fatalf("mkdir legacy config dir: %v", err)
	}
	if err := os.MkdirAll(to, 0o700); err != nil {
		t.Fatalf("mkdir target config dir: %v", err)
	}
	testutil.WriteFile(t, from, "config.yaml", "legacy\n")
	testutil.WriteFile(t, to, "config.yaml", "existing\n")

	ui := termio.NewTestMock(t)
	identity := git.Identity{RepoSlug: "agents-sandbox-bbb", LegacyRepoSlugs: []string{"workspace-aaa"}}

	if err := migrateLegacyConfigDirs(identity, &ui); err != nil {
		t.Fatalf("migrateLegacyConfigDirs: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(to, "config.yaml"))
	if err != nil {
		t.Fatalf("read target config: %v", err)
	}
	if string(body) != "existing\n" {
		t.Errorf("target config = %q, want it untouched", string(body))
	}
	if _, err := os.Stat(filepath.Join(from, "config.yaml")); err != nil {
		t.Errorf("legacy config removed although the target existed: %v", err)
	}
	var sawKept bool
	for _, call := range ui.VerboseCalls {
		if strings.Contains(call, "kept existing config dir") {
			sawKept = true
		}
	}
	if !sawKept {
		t.Errorf("expected the kept-config-dir verbose note; got %v", ui.VerboseCalls)
	}
}

// TestAdoptLegacyIdentityRecheckCorruptStateSkips covers the re-check under the
// lock: if the checkout state becomes unreadable while this run waited for the
// lock, adoption skips rather than proceeding on a document it cannot read, and
// leaves the legacy VM untouched for the caller's recovery path.
func TestAdoptLegacyIdentityRecheckCorruptStateSkips(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	seedLegacyState(t)

	adoptLockHook = func() {
		dir := state.KeyDir(adoptCheckoutKey())
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Errorf("mkdir checkout state dir: %v", err)
			return
		}
		testutil.WriteFile(t, dir, "state.yaml", `": [`)
	}
	t.Cleanup(func() { adoptLockHook = nil })

	client := &msb.MockMsbClient{GetSandboxFn: func(_ context.Context, name string) (msb.SandboxHandle, error) {
		t.Errorf("GetSandbox(%q) called despite an unreadable re-check", name)
		return nil, errors.New("GetSandbox must not be called")
	}}
	ui := termio.NewTestMock(t)

	adoption, err := AdoptLegacyIdentity(context.Background(), client, adoptIdentity(), adoptAgentName, true, &ui)
	if err != nil {
		t.Fatalf("a corrupt re-check must not abort the run: %v", err)
	}
	if adoption.Pending || adoption.Applied {
		t.Errorf("adoption = %+v, want nothing pending", adoption)
	}
}

// TestDestroyLegacyVMsErrorsOnUnreadableSlug covers the enumeration failure: an
// unreadable legacy slug directory aborts the teardown rather than moving state
// with an unknown legacy VM possibly left running.
func TestDestroyLegacyVMsErrorsOnUnreadableSlug(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	testutil.WritePath(t, state.SlugDir(adoptRepoSlug), "not a directory")

	ui := termio.NewTestMock(t)
	err := destroyLegacyVMs(context.Background(), &msb.MockMsbClient{}, adoptRepoSlug, adoptAgentName, &ui)
	if err == nil {
		t.Fatal("expected an unreadable slug to fail the teardown")
	}
	if !strings.Contains(err.Error(), "list legacy state") {
		t.Errorf("error = %v, want the enumeration failure", err)
	}
	if !errors.As(err, new(*os.PathError)) {
		t.Errorf("error = %v, want it to preserve the filesystem cause", err)
	}
}

// TestMigrateLegacyConfigDirsSkipsRepoSlugAndMissingSource covers the two skip
// branches: a legacy entry equal to the repo slug needs no move, and a legacy
// slug with no config directory has nothing to move.
func TestMigrateLegacyConfigDirsSkipsRepoSlugAndMissingSource(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	configpaths.WithRealConfigPaths(t)
	paths := configpaths.Get()

	// The repo-slug config dir exists, so an entry equal to the repo slug is
	// only a no-op if it is skipped outright: without the skip, from==to and the
	// code reports a spurious "kept existing" note.
	if err := os.MkdirAll(paths.UserSlugConfigDir("agents-sandbox-bbb"), 0o700); err != nil {
		t.Fatalf("mkdir repo-slug config dir: %v", err)
	}

	ui := termio.NewTestMock(t)
	identity := git.Identity{
		RepoSlug:        "agents-sandbox-bbb",
		LegacyRepoSlugs: []string{"agents-sandbox-bbb", "workspace-absent"},
	}
	if err := migrateLegacyConfigDirs(identity, &ui); err != nil {
		t.Fatalf("migrateLegacyConfigDirs: %v", err)
	}
	if len(ui.VerboseCalls) != 0 {
		t.Errorf("a repo-slug or missing legacy entry must be silent; got %v", ui.VerboseCalls)
	}
}

// TestMigrateLegacyConfigDirsContinuesPastRepoSlug pins loop order: a legacy
// entry equal to the repo slug is skipped, but the scan continues, so a later
// legacy identity's config directory is still moved onto the repo slug.
func TestMigrateLegacyConfigDirsContinuesPastRepoSlug(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	configpaths.WithRealConfigPaths(t)
	paths := configpaths.Get()

	from := paths.UserSlugConfigDir("workspace-aaa")
	if err := os.MkdirAll(from, 0o700); err != nil {
		t.Fatalf("mkdir legacy config dir: %v", err)
	}
	testutil.WriteFile(t, from, "config.yaml", "x: 1\n")

	ui := termio.NewTestMock(t)
	identity := git.Identity{
		RepoSlug:        "agents-sandbox-bbb",
		LegacyRepoSlugs: []string{"agents-sandbox-bbb", "workspace-aaa"},
	}
	if err := migrateLegacyConfigDirs(identity, &ui); err != nil {
		t.Fatalf("migrateLegacyConfigDirs: %v", err)
	}
	if _, err := os.Stat(filepath.Join(paths.UserSlugConfigDir("agents-sandbox-bbb"), "config.yaml")); err != nil {
		t.Errorf("a later legacy config dir was not moved past the repo-slug entry: %v", err)
	}
}

// TestMigrateLegacyConfigDirsContinuesPastMissingSource pins loop order on the
// missing source: a legacy identity with no config directory is skipped, but the
// scan continues, so a later present legacy directory is still moved.
func TestMigrateLegacyConfigDirsContinuesPastMissingSource(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	configpaths.WithRealConfigPaths(t)
	paths := configpaths.Get()

	from := paths.UserSlugConfigDir("workspace-aaa")
	if err := os.MkdirAll(from, 0o700); err != nil {
		t.Fatalf("mkdir legacy config dir: %v", err)
	}
	testutil.WriteFile(t, from, "config.yaml", "x: 1\n")

	ui := termio.NewTestMock(t)
	identity := git.Identity{
		RepoSlug:        "agents-sandbox-bbb",
		LegacyRepoSlugs: []string{"workspace-absent", "workspace-aaa"},
	}
	if err := migrateLegacyConfigDirs(identity, &ui); err != nil {
		t.Fatalf("migrateLegacyConfigDirs: %v", err)
	}
	if _, err := os.Stat(filepath.Join(paths.UserSlugConfigDir("agents-sandbox-bbb"), "config.yaml")); err != nil {
		t.Errorf("a later present legacy config dir was not moved past a missing source: %v", err)
	}
}

// TestMigrateLegacyConfigDirsReportsEveryKeptTarget pins that an existing target
// leaves every legacy directory in place, one note per legacy identity: the scan
// reports each rather than stopping at the first.
func TestMigrateLegacyConfigDirsReportsEveryKeptTarget(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	configpaths.WithRealConfigPaths(t)
	paths := configpaths.Get()

	if err := os.MkdirAll(paths.UserSlugConfigDir("agents-sandbox-bbb"), 0o700); err != nil {
		t.Fatalf("mkdir target config dir: %v", err)
	}
	for _, slug := range []string{"workspace-aaa", "workspace-ccc"} {
		from := paths.UserSlugConfigDir(slug)
		if err := os.MkdirAll(from, 0o700); err != nil {
			t.Fatalf("mkdir legacy config dir %s: %v", slug, err)
		}
		testutil.WriteFile(t, from, "config.yaml", "x: 1\n")
	}

	ui := termio.NewTestMock(t)
	identity := git.Identity{
		RepoSlug:        "agents-sandbox-bbb",
		LegacyRepoSlugs: []string{"workspace-aaa", "workspace-ccc"},
	}
	if err := migrateLegacyConfigDirs(identity, &ui); err != nil {
		t.Fatalf("migrateLegacyConfigDirs: %v", err)
	}
	var kept int
	for _, call := range ui.VerboseCalls {
		if strings.Contains(call, "kept existing config dir") {
			kept++
		}
	}
	if kept != 2 {
		t.Errorf("kept-config-dir notes = %d, want one per legacy identity; got %v", kept, ui.VerboseCalls)
	}
}

// TestMigrateLegacyConfigDirsReportsRenameFailure covers the rename failure: a
// failed config move is reported, not silently swallowed.
func TestMigrateLegacyConfigDirsReportsRenameFailure(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	configpaths.WithRealConfigPaths(t)
	paths := configpaths.Get()

	from := paths.UserSlugConfigDir("workspace-aaa")
	if err := os.MkdirAll(from, 0o700); err != nil {
		t.Fatalf("mkdir legacy config dir: %v", err)
	}
	testutil.WriteFile(t, from, "config.yaml", "x: 1\n")

	renameConfigDir = func(_, _ string) error { return errRenameBoom }
	t.Cleanup(func() { renameConfigDir = os.Rename })

	ui := termio.NewTestMock(t)
	identity := git.Identity{RepoSlug: "agents-sandbox-bbb", LegacyRepoSlugs: []string{"workspace-aaa"}}
	err := migrateLegacyConfigDirs(identity, &ui)
	if err == nil {
		t.Fatal("expected a rename failure to be reported")
	}
	if !strings.Contains(err.Error(), "move config dir") {
		t.Errorf("error = %v, want the config-move failure", err)
	}
	if !errors.Is(err, errRenameBoom) {
		t.Errorf("error = %v, want it to wrap the rename cause", err)
	}
}

// TestAdoptLegacyIdentityConfigMoveFailurePropagates covers the caller's
// propagation: a failed config move aborts the adoption after the state move.
func TestAdoptLegacyIdentityConfigMoveFailurePropagates(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	configpaths.WithRealConfigPaths(t)
	paths := configpaths.Get()

	from := paths.UserSlugConfigDir("workspace-aaa")
	if err := os.MkdirAll(from, 0o700); err != nil {
		t.Fatalf("mkdir legacy config dir: %v", err)
	}
	testutil.WriteFile(t, from, "config.yaml", "x: 1\n")
	if err := seedHomeState(
		state.Key{Slug: "workspace-aaa", Agent: adoptAgentName},
		state.HomeState{HomeVolume: "home-legacy"},
	); err != nil {
		t.Fatalf("seed legacy state: %v", err)
	}

	renameConfigDir = func(_, _ string) error { return errRenameBoom }
	t.Cleanup(func() { renameConfigDir = os.Rename })

	client := &msb.MockMsbClient{GetSandboxFn: func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "gone"}
	}}
	identity := adoptIdentity()
	identity.LegacyRepoSlugs = []string{"workspace-aaa"}
	ui := termio.NewTestMock(t)

	_, err := AdoptLegacyIdentity(context.Background(), client, identity, adoptAgentName, true, &ui)
	if err == nil {
		t.Fatal("expected the config-move failure to abort adoption")
	}
	if !strings.Contains(err.Error(), "move config dir") {
		t.Errorf("error = %v, want the propagated config-move failure", err)
	}
	if !errors.Is(err, errRenameBoom) {
		t.Errorf("error = %v, want it to wrap the rename cause", err)
	}
}

// TestAdoptLegacyIdentityDryRunReturnsFoundLegacySlug covers the dry-run slug: a
// bare root's pending state can live under its path-derived legacy slug, so a dry
// run must report that slug (not the repo slug) for the caller to resolve against.
func TestAdoptLegacyIdentityDryRunReturnsFoundLegacySlug(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	const legacySlug = "workspace-aaa"

	identity := adoptIdentity()
	identity.LegacyRepoSlugs = []string{legacySlug}
	if err := seedHomeState(
		state.Key{Slug: legacySlug, Agent: adoptAgentName},
		state.HomeState{HomeVolume: "home-legacy"},
	); err != nil {
		t.Fatalf("seed bare-root legacy state: %v", err)
	}

	client := &msb.MockMsbClient{GetSandboxFn: func(_ context.Context, name string) (msb.SandboxHandle, error) {
		return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "no VM for " + name}
	}}
	ui := termio.NewTestMock(t)

	got, err := AdoptLegacyIdentity(context.Background(), client, identity, adoptAgentName, false, &ui)
	if err != nil {
		t.Fatalf("AdoptLegacyIdentity: %v", err)
	}
	if got.Slug != legacySlug {
		t.Errorf("dry-run Slug = %q, want the legacy slug %q that holds the state", got.Slug, legacySlug)
	}
	if !got.Pending || got.Applied {
		t.Errorf("adoption = %+v, want pending and not applied", got)
	}
	notice := strings.Join(ui.WarnCalls, "\n")
	if !strings.Contains(notice, state.SlugDir(legacySlug)) {
		t.Errorf("notice missing the extra legacy state row for %s:\n%s", legacySlug, notice)
	}
	legacyConfigDir := configpaths.Get().UserSlugConfigDir(legacySlug)
	if !strings.Contains(notice, "config dir") || !strings.Contains(notice, legacyConfigDir) {
		t.Errorf("notice missing the legacy config-dir row %q:\n%s", legacyConfigDir, notice)
	}
}

// TestAdoptLegacyIdentityDestroyFailureLeavesStateForRetry covers resumability: a
// bare root can have state under both the origin slug and its path slug, and if
// one source's VM teardown fails, the migration must abort with every legacy state
// file still in place so the next run resumes and adopts the remaining source
// rather than stranding it.
func TestAdoptLegacyIdentityDestroyFailureLeavesStateForRetry(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	const legacySlug = "workspace-aaa"
	identity := adoptIdentity()
	identity.LegacyRepoSlugs = []string{legacySlug}

	// The requested agent's pending state is under the origin slug; a sibling
	// agent's state sits under the bare-root legacy slug, whose VM teardown fails.
	if err := seedHomeState(adoptRepoKey(), state.HomeState{HomeVolume: adoptLegacyVolume}); err != nil {
		t.Fatalf("seed origin state: %v", err)
	}
	piLegacyKey := state.Key{Slug: legacySlug, Agent: "pi"}
	if err := seedHomeState(piLegacyKey, state.HomeState{HomeVolume: "home-pi"}); err != nil {
		t.Fatalf("seed bare-root legacy state: %v", err)
	}

	failingClient := &msb.MockMsbClient{GetSandboxFn: func(_ context.Context, name string) (msb.SandboxHandle, error) {
		if strings.Contains(name, legacySlug) {
			return nil, errors.New("teardown boom")
		}
		return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "gone"}
	}}
	ui := termio.NewTestMock(t)

	if _, err := AdoptLegacyIdentity(
		context.Background(),
		failingClient,
		identity,
		adoptAgentName,
		true,
		&ui,
	); err == nil {
		t.Fatal("expected the sibling VM teardown failure to abort the migration")
	}
	if statePresent(t, adoptCheckoutKey()) {
		t.Error("a destroy failure must not have moved state: the checkout state file exists")
	}
	if !statePresent(t, adoptRepoKey()) || !statePresent(t, piLegacyKey) {
		t.Error("a destroy failure must leave every legacy state file in place for retry")
	}

	// Retry with every VM gone: both sources are adopted now.
	okClient := &msb.MockMsbClient{GetSandboxFn: func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "gone"}
	}}
	retryUI := termio.NewTestMock(t)

	got, err := AdoptLegacyIdentity(context.Background(), okClient, identity, adoptAgentName, true, &retryUI)
	if err != nil {
		t.Fatalf("retry AdoptLegacyIdentity: %v", err)
	}
	if !got.Applied {
		t.Errorf("retry = %+v, want it applied", got)
	}
	if !statePresent(t, adoptCheckoutKey()) || !statePresent(t, state.Key{Slug: adoptCheckoutSlug, Agent: "pi"}) {
		t.Error("retry did not adopt every legacy source")
	}
}

// TestAdoptLegacyIdentityAdoptsLiveLegacyVMWithoutState covers the lost-state
// case (issue #219): no legacy state file, but a live legacy project VM. The VM
// is itself a legacy identity, so adoption destroys it and records its mounted
// home volume durably in the checkout state before the destroy, for home
// resolution to reuse — instead of treating the checkout as brand new and
// minting a fresh volume.
func TestAdoptLegacyIdentityAdoptsLiveLegacyVMWithoutState(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	const legacyVolume = "agents-sandbox-home-widget-aaa-opencode-9"
	handle := &msb.MockSandboxHandle{
		Name_: adoptLegacyVMName(),
		Mounts: map[string]msbSdk.MountConfig{
			mounts.VMHomeDir: msbSdk.Mount.Named(legacyVolume, msbSdk.MountOptions{}),
		},
	}
	client := &msb.MockMsbClient{GetSandboxFn: func(_ context.Context, name string) (msb.SandboxHandle, error) {
		if name == adoptLegacyVMName() {
			return handle, nil
		}
		return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "gone"}
	}}
	ui := termio.NewTestMock(t)

	got, err := AdoptLegacyIdentity(context.Background(), client, adoptIdentity(), adoptAgentName, true, &ui)
	if err != nil {
		t.Fatalf("AdoptLegacyIdentity: %v", err)
	}
	if !handle.Destroyed {
		t.Error("live legacy VM was not destroyed")
	}
	want := IdentityAdoption{Slug: adoptCheckoutSlug, Pending: true, Applied: true}
	if got != want {
		t.Errorf("adoption = %+v, want %+v", got, want)
	}
	// The VM was the only record of its home volume and is now gone, so adoption
	// must have persisted the volume first.
	st, readErr := state.ReadState(adoptCheckoutKey())
	if readErr != nil {
		t.Fatalf("adoption did not record the recovered volume: %v", readErr)
	}
	if st.RecoveredHomeVolume != legacyVolume {
		t.Errorf("RecoveredHomeVolume = %q, want %q", st.RecoveredHomeVolume, legacyVolume)
	}
}

// TestAdoptLegacyIdentityPersistsVolumeBeforeDestroyFails pins the ordering the
// #244 review required: the recovered volume is durable before the legacy VM is
// destroyed, so a destroy failure (or a caller that discards the result) cannot
// lose it — and the marker keeps the trigger live so the teardown is retried.
func TestAdoptLegacyIdentityPersistsVolumeBeforeDestroyFails(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	const legacyVolume = "agents-sandbox-home-widget-aaa-opencode-9"
	handle := &msb.MockSandboxHandle{
		Name_:      adoptLegacyVMName(),
		DestroyErr: errors.New("destroy boom"),
		Mounts: map[string]msbSdk.MountConfig{
			mounts.VMHomeDir: msbSdk.Mount.Named(legacyVolume, msbSdk.MountOptions{}),
		},
	}
	client := &msb.MockMsbClient{GetSandboxFn: func(_ context.Context, name string) (msb.SandboxHandle, error) {
		if name == adoptLegacyVMName() {
			return handle, nil
		}
		return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "gone"}
	}}
	ui := termio.NewTestMock(t)

	if _, err := AdoptLegacyIdentity(
		context.Background(),
		client,
		adoptIdentity(),
		adoptAgentName,
		true,
		&ui,
	); err == nil {
		t.Fatal("expected the destroy failure to abort adoption")
	}
	st, readErr := state.ReadState(adoptCheckoutKey())
	if readErr != nil || st.RecoveredHomeVolume != legacyVolume {
		t.Fatalf("volume not persisted before the destroy: %+v (err %v)", st, readErr)
	}

	// Retry with a working destroy: the marker keeps the trigger live, so the
	// leftover legacy VM is torn down and the marker survives for recovery.
	handle.DestroyErr = nil
	retryUI := termio.NewTestMock(t)
	retry, err := AdoptLegacyIdentity(context.Background(), client, adoptIdentity(), adoptAgentName, true, &retryUI)
	if err != nil {
		t.Fatalf("retry AdoptLegacyIdentity: %v", err)
	}
	if !handle.Destroyed {
		t.Error("retry did not destroy the leftover legacy VM")
	}
	if !retry.Pending || !retry.Applied {
		t.Errorf("retry adoption = %+v, want pending and applied", retry)
	}
	st, readErr = state.ReadState(adoptCheckoutKey())
	if readErr != nil || st.RecoveredHomeVolume != legacyVolume {
		t.Errorf("recovered volume lost on retry: %+v (err %v)", st, readErr)
	}
}

// TestAdoptLegacyIdentityDryRunLiveLegacyVMIsReadOnly pins the dry run over a
// live legacy VM: the notice names the VM's home volume, nothing is destroyed,
// nothing is recorded, and the caller keeps the legacy slug to resolve against.
func TestAdoptLegacyIdentityDryRunLiveLegacyVMIsReadOnly(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	const legacyVolume = "agents-sandbox-home-widget-aaa-opencode-9"
	handle := &msb.MockSandboxHandle{
		Name_: adoptLegacyVMName(),
		Mounts: map[string]msbSdk.MountConfig{
			mounts.VMHomeDir: msbSdk.Mount.Named(legacyVolume, msbSdk.MountOptions{}),
		},
	}
	client := &msb.MockMsbClient{GetSandboxFn: func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return handle, nil
	}}
	ui := termio.NewTestMock(t)

	got, err := AdoptLegacyIdentity(context.Background(), client, adoptIdentity(), adoptAgentName, false, &ui)
	if err != nil {
		t.Fatalf("AdoptLegacyIdentity: %v", err)
	}
	if handle.Destroyed {
		t.Error("legacy VM destroyed during a dry run")
	}
	notice := strings.Join(ui.WarnCalls, "\n")
	if !strings.Contains(notice, "Would migrate") || !strings.Contains(notice, legacyVolume) {
		t.Errorf("dry-run notice missing the would-headline or the volume:\n%s", notice)
	}
	want := IdentityAdoption{Slug: adoptRepoSlug, Pending: true}
	if got != want {
		t.Errorf("adoption = %+v, want %+v", got, want)
	}
	if statePresent(t, adoptCheckoutKey()) {
		t.Error("a dry run must not record a checkout state")
	}
}

// TestAdoptLegacyIdentityLiveLegacyVMMountsErrorFailsClosed covers the fail-closed
// rule: a live legacy VM whose mounts cannot be read might hold the user's only
// home volume, so adoption aborts rather than destroying it blind.
func TestAdoptLegacyIdentityLiveLegacyVMMountsErrorFailsClosed(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	handle := &msb.MockSandboxHandle{Name_: adoptLegacyVMName(), MountsErr: errors.New("mounts boom")}
	client := &msb.MockMsbClient{GetSandboxFn: func(_ context.Context, _ string) (msb.SandboxHandle, error) {
		return handle, nil
	}}
	ui := termio.NewTestMock(t)

	if _, err := AdoptLegacyIdentity(
		context.Background(),
		client,
		adoptIdentity(),
		adoptAgentName,
		true,
		&ui,
	); err == nil {
		t.Fatal("expected an unreadable legacy VM mount to fail adoption")
	}
	if handle.Destroyed {
		t.Error("legacy VM destroyed despite an unreadable mount")
	}
}

// TestAdoptLegacyIdentityClaimsSharedVolumeOnce covers the two-clone race the
// #244 review found: clone A records a marker for a shared legacy VM's home
// volume but its destroy fails, so clone B can still adopt the surviving VM.
// Only one checkout may own the volume: B must not record a marker, and the
// claim must name A.
func TestAdoptLegacyIdentityClaimsSharedVolumeOnce(t *testing.T) {
	configpaths.WithMockConfigPaths(t)

	const legacyVolume = "agents-sandbox-home-widget-aaa-opencode-5"
	identityA := git.Identity{RepoSlug: adoptRepoSlug, CheckoutSlug: "checkout-aaa"}
	identityB := git.Identity{RepoSlug: adoptRepoSlug, CheckoutSlug: "checkout-bbb"}
	checkoutA := state.Key{Slug: "checkout-aaa", Agent: adoptAgentName}
	checkoutB := state.Key{Slug: "checkout-bbb", Agent: adoptAgentName}

	legacyMount := map[string]msbSdk.MountConfig{
		mounts.VMHomeDir: msbSdk.Mount.Named(legacyVolume, msbSdk.MountOptions{}),
	}
	// Clone A claims the volume and records the marker, then its destroy fails.
	failing := &msb.MockSandboxHandle{
		Name_:      adoptLegacyVMName(),
		DestroyErr: errors.New("destroy boom"),
		Mounts:     legacyMount,
	}
	clientA := &msb.MockMsbClient{GetSandboxFn: func(_ context.Context, name string) (msb.SandboxHandle, error) {
		if name == adoptLegacyVMName() {
			return failing, nil
		}
		return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "gone"}
	}}
	uiA := termio.NewTestMock(t)
	if _, err := AdoptLegacyIdentity(
		context.Background(), clientA, identityA, adoptAgentName, true, &uiA,
	); err == nil {
		t.Fatal("expected clone A's destroy failure to abort adoption")
	}
	if stA, err := state.ReadState(checkoutA); err != nil || stA.RecoveredHomeVolume != legacyVolume {
		t.Fatalf("clone A marker = %+v (err %v), want %q", stA, err, legacyVolume)
	}

	// Clone B adopts the surviving legacy VM successfully; it must not record the
	// same volume, which clone A owns.
	succeeding := &msb.MockSandboxHandle{Name_: adoptLegacyVMName(), Mounts: legacyMount}
	clientB := &msb.MockMsbClient{GetSandboxFn: func(_ context.Context, name string) (msb.SandboxHandle, error) {
		if name == adoptLegacyVMName() {
			return succeeding, nil
		}
		return nil, &msbSdk.Error{Kind: msbSdk.ErrSandboxNotFound, Message: "gone"}
	}}
	uiB := termio.NewTestMock(t)
	if _, err := AdoptLegacyIdentity(
		context.Background(), clientB, identityB, adoptAgentName, true, &uiB,
	); err != nil {
		t.Fatalf("clone B adoption: %v", err)
	}
	if !succeeding.Destroyed {
		t.Error("clone B did not tear down the leftover legacy VM")
	}
	if stB, readErr := state.ReadState(checkoutB); readErr == nil && stB.RecoveredHomeVolume != "" {
		t.Errorf("clone B recorded the shared volume: %+v", stB)
	}

	root, err := state.ReadRootState()
	if err != nil {
		t.Fatalf("ReadRootState: %v", err)
	}
	claim, ok := root.AdoptedHomeVolume(adoptRepoSlug, adoptAgentName)
	if !ok || claim.CheckoutSlug != identityA.CheckoutSlug || claim.Volume != legacyVolume {
		t.Errorf("claim = %+v (present %v), want %q owning %q", claim, ok, identityA.CheckoutSlug, legacyVolume)
	}
}

// TestAdoptionNoticeSkipsLegacySlugThatIsTheCheckoutSlug: a bare root whose old
// path slug already is its checkout slug (#433) moves no state from it, so the
// notice lists no "X -> X" state row, but still names the config dir move. A
// second legacy slug that is not the checkout slug still gets its own row, so the
// skip applies to the matching entry only rather than aborting the listing.
func TestAdoptionNoticeSkipsLegacySlugThatIsTheCheckoutSlug(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	const otherLegacySlug = "widget-legacy"
	identity := adoptIdentity()
	identity.LegacyRepoSlugs = []string{adoptCheckoutSlug, otherLegacySlug}

	notice := adoptionNotice(identity, adoptAgentName, adoptLegacyVolume, true)

	selfMove := state.SlugDir(adoptCheckoutSlug) + "  ->  " + state.SlugDir(adoptCheckoutSlug)
	if strings.Contains(notice, selfMove) {
		t.Errorf("notice lists a state move onto itself:\n%s", notice)
	}
	otherMove := state.SlugDir(otherLegacySlug) + "  ->  " + state.SlugDir(adoptCheckoutSlug)
	if !strings.Contains(notice, otherMove) {
		t.Errorf("notice missing the state move %q for a later legacy slug:\n%s", otherMove, notice)
	}
	configMove := configpaths.Get().UserSlugConfigDir(adoptCheckoutSlug) + "  ->  " +
		configpaths.Get().UserSlugConfigDir(adoptRepoSlug)
	if !strings.Contains(notice, configMove) {
		t.Errorf("notice missing the config dir move %q:\n%s", configMove, notice)
	}
}
