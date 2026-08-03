package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"

	msb "github.com/superradcompany/microsandbox/sdk/go"

	"gitlab.inoio.de/inoio/opencode-msb/internal/stdio"
)

// mockMsbClient is a test double for msbClient.
type mockMsbClient struct {
	sandboxes []msbSandboxHandle
	volumes   []msbVolumeHandle
	images    []msbImageHandle

	createdSandboxes []string
	removedSandboxes []string
	removedVolumes   []string
	removedImages    []struct {
		ref   string
		force bool
	}
	loadedImages []string

	ensureInstalledErr error
	getSandboxErr      error
	createSandboxErr   error
	listSandboxesErr   error
	getVolumeErr       error
	createVolumeErr    error
	listVolumesErr     error
	listImagesErr      error
	removeSandboxErr   error
	removeVolumeErr    error
	removeImageErr     error
	imageGetErr        error
	imageLoadErr       error

	createdSandbox msbSandbox
	gotSandbox     msbSandboxHandle
	gotVolume      msbVolumeHandle
}

func (m *mockMsbClient) EnsureInstalled(_ context.Context) error {
	return m.ensureInstalledErr
}

func (m *mockMsbClient) GetSandbox(_ context.Context, name string) (msbSandboxHandle, error) {
	if m.getSandboxErr != nil {
		return nil, m.getSandboxErr
	}
	if m.gotSandbox != nil {
		return m.gotSandbox, nil
	}
	return nil, errors.New("sandbox not found: " + name)
}

func (m *mockMsbClient) CreateSandbox(_ context.Context, name string, _ ...msb.SandboxOption) (msbSandbox, error) {
	m.createdSandboxes = append(m.createdSandboxes, name)
	if m.createSandboxErr != nil {
		return nil, m.createSandboxErr
	}
	if m.createdSandbox != nil {
		return m.createdSandbox, nil
	}
	return &mockSandbox{name: name}, nil
}

func (m *mockMsbClient) ListSandboxes(_ context.Context) ([]msbSandboxHandle, error) {
	if m.listSandboxesErr != nil {
		return nil, m.listSandboxesErr
	}
	return m.sandboxes, nil
}

func (m *mockMsbClient) RemoveSandbox(_ context.Context, name string) error {
	if m.removeSandboxErr != nil {
		return m.removeSandboxErr
	}
	m.removedSandboxes = append(m.removedSandboxes, name)
	return nil
}

func (m *mockMsbClient) GetVolume(_ context.Context, name string) (msbVolumeHandle, error) {
	if m.getVolumeErr != nil {
		return nil, m.getVolumeErr
	}
	if m.gotVolume != nil {
		return m.gotVolume, nil
	}
	return nil, errors.New("volume not found: " + name)
}

func (m *mockMsbClient) CreateVolume(_ context.Context, name string, _ ...msb.VolumeOption) (msbVolumeHandle, error) {
	if m.createVolumeErr != nil {
		return nil, m.createVolumeErr
	}
	return mockVolumeHandle{name: name}, nil
}

func (m *mockMsbClient) ListVolumes(_ context.Context) ([]msbVolumeHandle, error) {
	if m.listVolumesErr != nil {
		return nil, m.listVolumesErr
	}
	return m.volumes, nil
}

func (m *mockMsbClient) RemoveVolume(_ context.Context, name string) error {
	if m.removeVolumeErr != nil {
		return m.removeVolumeErr
	}
	m.removedVolumes = append(m.removedVolumes, name)
	return nil
}

func (m *mockMsbClient) ImageGet(_ context.Context, _ string) error {
	return m.imageGetErr
}

func (m *mockMsbClient) ImageList(_ context.Context) ([]msbImageHandle, error) {
	if m.listImagesErr != nil {
		return nil, m.listImagesErr
	}
	return m.images, nil
}

func (m *mockMsbClient) ImageRemove(_ context.Context, ref string, force bool) error {
	if m.removeImageErr != nil {
		return m.removeImageErr
	}
	m.removedImages = append(m.removedImages, struct {
		ref   string
		force bool
	}{ref: ref, force: force})
	return nil
}

func (m *mockMsbClient) ImageLoad(_ context.Context, ref string, _ io.Reader) error {
	m.loadedImages = append(m.loadedImages, ref)
	return m.imageLoadErr
}

// mockSandboxHandle implements msbSandboxHandle for tests.
type mockSandboxHandle struct {
	name       string
	status     msb.SandboxStatus
	updatedAt  time.Time
	image      string
	connect    msbSandbox
	start      msbSandbox
	refresh    msbSandboxHandle
	connectErr error
	startErr   error
	stopErr    error
	killErr    error
	removeErr  error
	refreshErr error
}

func (m mockSandboxHandle) Name() string {
	return m.name
}

func (m mockSandboxHandle) Status() msb.SandboxStatus {
	return m.status
}

func (m mockSandboxHandle) UpdatedAt() time.Time {
	return m.updatedAt
}

func (m mockSandboxHandle) Image() string {
	return m.image
}

func (m mockSandboxHandle) Connect(_ context.Context) (msbSandbox, error) {
	if m.connectErr != nil {
		return nil, m.connectErr
	}
	if m.connect != nil {
		return m.connect, nil
	}
	return &mockSandbox{name: m.name}, nil
}

func (m mockSandboxHandle) Refresh(_ context.Context) (msbSandboxHandle, error) {
	if m.refreshErr != nil {
		return nil, m.refreshErr
	}
	if m.refresh != nil {
		return m.refresh, nil
	}
	return m, nil
}

func (m mockSandboxHandle) Start(_ context.Context) (msbSandbox, error) {
	if m.startErr != nil {
		return nil, m.startErr
	}
	if m.start != nil {
		return m.start, nil
	}
	return &mockSandbox{name: m.name}, nil
}

func (m mockSandboxHandle) Stop(_ context.Context, _ ...msb.StopOption) error {
	return m.stopErr
}

func (m mockSandboxHandle) Kill(_ context.Context, _ ...msb.KillOption) error {
	return m.killErr
}

func (m mockSandboxHandle) Remove(_ context.Context) error {
	return m.removeErr
}

// mockSandbox implements msbSandbox for tests.
type mockSandbox struct {
	name       string
	fsValue    any
	shellOut   map[string]shellResult
	shellErr   error
	execOut    map[string]shellResult
	execErr    error
	attachCode int
	attachErr  error
	detachErr  error
	stopErr    error
	closeErr   error
}

func (m *mockSandbox) FS() sandboxFS {
	if f, ok := m.fsValue.(sandboxFS); ok {
		return f
	}
	return nil
}

func (m *mockSandbox) Shell(_ context.Context, command string, _ ...msb.ExecOption) (shellResult, error) {
	if m.shellErr != nil {
		return nil, m.shellErr
	}
	if out, ok := m.shellOut[command]; ok {
		return out, nil
	}
	return &mockShellResult{success: true}, nil
}

func (m *mockSandbox) Exec(_ context.Context, command string, args []string, _ ...msb.ExecOption) (shellResult, error) {
	if m.execErr != nil {
		return nil, m.execErr
	}
	key := command + " " + strings.Join(args, " ")
	if out, ok := m.execOut[key]; ok {
		return out, nil
	}
	return &mockShellResult{success: true}, nil
}

func (m *mockSandbox) Attach(_ context.Context, _ string, _ ...string) (int, error) {
	return m.attachCode, m.attachErr
}

func (m *mockSandbox) Detach(_ context.Context) error {
	return m.detachErr
}

func (m *mockSandbox) Stop(_ context.Context, _ ...msb.StopOption) error {
	return m.stopErr
}

func (m *mockSandbox) Close() error {
	return m.closeErr
}

// mockShellResult implements shellResult for tests.
type mockShellResult struct {
	success     bool
	exitCode    int
	stdout      string
	stderr      string
	stdoutBytes []byte
}

func (m *mockShellResult) Success() bool {
	return m.success
}

func (m *mockShellResult) ExitCode() int {
	return m.exitCode
}

func (m *mockShellResult) Stdout() string {
	return m.stdout
}

func (m *mockShellResult) Stderr() string {
	return m.stderr
}

func (m *mockShellResult) StdoutBytes() []byte {
	if m.stdoutBytes != nil {
		return m.stdoutBytes
	}
	return []byte(m.stdout)
}

// mockFs implements sandboxFS for tests.
type mockFs struct {
	files   map[string][]byte
	list    []msb.FsEntry
	readErr error
	listErr error
}

func (f *mockFs) Mkdir(_ context.Context, _ string) error           { return nil }
func (f *mockFs) Write(_ context.Context, _ string, _ []byte) error { return nil }
func (f *mockFs) Read(_ context.Context, path string) ([]byte, error) {
	if f.readErr != nil {
		return nil, f.readErr
	}
	if data, ok := f.files[path]; ok {
		return data, nil
	}
	return nil, fmt.Errorf("file not found: %s", path)
}
func (f *mockFs) List(_ context.Context, _ string) ([]msb.FsEntry, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.list, nil
}
func (f *mockFs) Remove(_ context.Context, _ string) error { return nil }
func (f *mockFs) Exists(_ context.Context, path string) (bool, error) {
	_, ok := f.files[path]
	return ok, nil
}
func (f *mockFs) Stat(_ context.Context, _ string) (*msb.FsStat, error) { return nil, nil }
func (f *mockFs) ReadString(_ context.Context, path string) (string, error) {
	if d, ok := f.files[path]; ok {
		return string(d), nil
	}
	return "", fmt.Errorf("file not found: %s", path)
}
func (f *mockFs) ReadStream(_ context.Context, _ string) (*msb.FsReadStream, error) {
	return nil, nil
}

// mockVolumeHandle implements msbVolumeHandle for tests.
type mockVolumeHandle struct {
	name string
	path string
	kind msb.VolumeKind
}

func (m mockVolumeHandle) Name() string {
	return m.name
}

func (m mockVolumeHandle) Path() string {
	return m.path
}

func (m mockVolumeHandle) Kind() msb.VolumeKind {
	if m.kind == "" {
		return msb.VolumeKindDir
	}
	return m.kind
}

// mockImageHandle implements msbImageHandle for tests.
type mockImageHandle struct {
	ref            string
	manifestDigest string
}

func (m mockImageHandle) Reference() string {
	return m.ref
}

func (m mockImageHandle) ManifestDigest() string {
	return m.manifestDigest
}

// mockDockerClient implements dockerClient for tests.
type mockDockerClient struct {
	removedImages []string
	removeErr     error
	perCallErrs   []error
	callCounter   int
}

func (m *mockDockerClient) ImageBuild(
	_ context.Context,
	_ io.Reader,
	_ client.ImageBuildOptions,
) (client.ImageBuildResult, error) {
	return client.ImageBuildResult{}, nil
}

func (m *mockDockerClient) ImageInspect(
	_ context.Context,
	_ string,
	_ ...client.ImageInspectOption,
) (client.ImageInspectResult, error) {
	return client.ImageInspectResult{}, nil
}

func (m *mockDockerClient) ImageSave(
	_ context.Context,
	_ []string,
	_ ...client.ImageSaveOption,
) (client.ImageSaveResult, error) {
	return io.NopCloser(bytes.NewReader(nil)), nil
}

func (m *mockDockerClient) ImageRemove(
	_ context.Context,
	imageID string,
	_ client.ImageRemoveOptions,
) (client.ImageRemoveResult, error) {
	// Check per-call errors first.
	if m.callCounter < len(m.perCallErrs) {
		err := m.perCallErrs[m.callCounter]
		m.callCounter++
		if err != nil {
			return client.ImageRemoveResult{}, err
		}
		m.removedImages = append(m.removedImages, imageID)
		return client.ImageRemoveResult{}, nil
	}
	// Fall back to global removeErr (checked first to maintain legacy behavior).
	if m.removeErr != nil {
		return client.ImageRemoveResult{}, m.removeErr
	}
	m.removedImages = append(m.removedImages, imageID)
	return client.ImageRemoveResult{}, nil
}

func (m *mockDockerClient) ImageTag(
	_ context.Context,
	_ client.ImageTagOptions,
) (client.ImageTagResult, error) {
	return client.ImageTagResult{}, nil
}

func (m *mockDockerClient) Close() error {
	return nil
}

func newMockUI() *stdio.Mock {
	return &stdio.Mock{}
}

func TestPruneStaleCascade_RemovesVMAndAllArtifacts(t *testing.T) {
	client := &mockMsbClient{}
	docker := &mockDockerClient{}
	ui := newMockUI()
	report := &StaleReport{}

	slug := "myproject"
	entry := StaleEntry{Name: "opencode-msb-vm-myproject-digest1", Slug: slug}
	homeBySlugDigest := map[string]map[string]string{
		slug: {
			"digest1": "opencode-msb-home-myproject-digest1",
			"":        "opencode-msb-home-myproject",
		},
	}
	msbImagesBySlug := map[string][]imageWithDigest{
		slug: {
			{ref: "opencode-msb/runner-myproject:digest1", digest: "digest1", isLatest: false},
			{ref: "opencode-msb/runner-myproject:latest", digest: "", isLatest: true},
		},
	}

	pruneStaleCascade(context.Background(), client, docker, entry, homeBySlugDigest, msbImagesBySlug, false, ui, report)

	if report.PrunedVMs != 1 {
		t.Errorf("PrunedVMs = %d, want 1", report.PrunedVMs)
	}
	if report.PrunedVolumes != 2 {
		t.Errorf("PrunedVolumes = %d, want 2", report.PrunedVolumes)
	}
	if report.PrunedMSBImages != 2 {
		t.Errorf("PrunedMSBImages = %d, want 2", report.PrunedMSBImages)
	}
	if report.PrunedDockerImages != 2 {
		t.Errorf("PrunedDockerImages = %d, want 2", report.PrunedDockerImages)
	}

	wantRemoved := []string{entry.Name}
	if len(client.removedSandboxes) != 1 || client.removedSandboxes[0] != entry.Name {
		t.Errorf("removed sandboxes = %v, want %v", client.removedSandboxes, wantRemoved)
	}

	wantVolumes := []string{"opencode-msb-home-myproject-digest1", "opencode-msb-home-myproject"}
	if len(client.removedVolumes) != len(wantVolumes) {
		t.Errorf("removed volumes = %v, want %v", client.removedVolumes, wantVolumes)
	}

	wantMSBImages := []string{"opencode-msb/runner-myproject:digest1", "opencode-msb/runner-myproject:latest"}
	if len(client.removedImages) != len(wantMSBImages) {
		t.Errorf("removed msb images count = %d, want %d", len(client.removedImages), len(wantMSBImages))
	}

	wantDockerImages := []string{"opencode-msb/runner-myproject:digest1", "opencode-msb/runner-myproject:latest"}
	if len(docker.removedImages) != len(wantDockerImages) {
		t.Errorf("removed docker images = %v, want %v", docker.removedImages, wantDockerImages)
	}
}

func TestPruneStaleCascade_DryRunDoesNotDelete(t *testing.T) {
	client := &mockMsbClient{}
	docker := &mockDockerClient{}
	ui := newMockUI()
	report := &StaleReport{}

	slug := "myproject"
	entry := StaleEntry{Name: "opencode-msb-vm-myproject-digest1", Slug: slug}
	homeBySlugDigest := map[string]map[string]string{
		slug: {"digest1": "opencode-msb-home-myproject-digest1"},
	}
	msbImagesBySlug := map[string][]imageWithDigest{
		slug: {{ref: "opencode-msb/runner-myproject:digest1", digest: "digest1", isLatest: false}},
	}

	pruneStaleCascade(context.Background(), client, docker, entry, homeBySlugDigest, msbImagesBySlug, true, ui, report)

	if report.PrunedVMs != 1 {
		t.Errorf("PrunedVMs = %d, want 1", report.PrunedVMs)
	}
	if report.PrunedVolumes != 1 {
		t.Errorf("PrunedVolumes = %d, want 1", report.PrunedVolumes)
	}
	if report.PrunedMSBImages != 1 {
		t.Errorf("PrunedMSBImages = %d, want 1", report.PrunedMSBImages)
	}
	if report.PrunedDockerImages != 1 {
		t.Errorf("PrunedDockerImages = %d, want 1", report.PrunedDockerImages)
	}

	total := len(client.removedSandboxes) + len(client.removedVolumes) +
		len(client.removedImages) + len(docker.removedImages)
	if total != 0 {
		t.Errorf("expected no deletion calls in dry run, got sandboxes=%v volumes=%v images=%v docker=%v",
			client.removedSandboxes, client.removedVolumes, client.removedImages, docker.removedImages)
	}
}

func TestPruneStaleCascade_RemoveErrorWarnsAndStopsCascade(t *testing.T) {
	client := &mockMsbClient{removeSandboxErr: errors.New("sandbox locked")}
	docker := &mockDockerClient{}
	ui := newMockUI()
	report := &StaleReport{}

	slug := "myproject"
	entry := StaleEntry{Name: "opencode-msb-vm-myproject-digest1", Slug: slug}
	homeBySlugDigest := map[string]map[string]string{
		slug: {"digest1": "opencode-msb-home-myproject-digest1"},
	}
	msbImagesBySlug := map[string][]imageWithDigest{
		slug: {{ref: "opencode-msb/runner-myproject:digest1", digest: "digest1", isLatest: false}},
	}

	pruneStaleCascade(context.Background(), client, docker, entry, homeBySlugDigest, msbImagesBySlug, false, ui, report)

	if report.PrunedVMs != 0 {
		t.Errorf("PrunedVMs = %d, want 0", report.PrunedVMs)
	}
	if len(ui.WarnCalls) == 0 {
		t.Error("expected a warning on sandbox removal error")
	}
	if len(client.removedVolumes)+len(client.removedImages)+len(docker.removedImages) != 0 {
		t.Error("expected cascade to stop after sandbox removal failure")
	}
}

func TestPruneActiveVMCleanup_KeepsMatchingDigestAndLatest(t *testing.T) {
	client := &mockMsbClient{}
	docker := &mockDockerClient{}
	ui := newMockUI()
	report := &StaleReport{}

	slug := "myproject"
	activeDigest := "digest2"
	homeBySlugDigest := map[string]map[string]string{
		slug: {
			"digest1": "opencode-msb-home-myproject-digest1",
			"digest2": "opencode-msb-home-myproject-digest2",
			"":        "opencode-msb-home-myproject",
		},
	}
	msbImagesBySlug := map[string][]imageWithDigest{
		slug: {
			{ref: "opencode-msb/runner-myproject:digest1", digest: "digest1", isLatest: false},
			{ref: "opencode-msb/runner-myproject:digest2", digest: "digest2", isLatest: false},
			{ref: "opencode-msb/runner-myproject:latest", digest: "", isLatest: true},
		},
	}

	pruneActiveVMCleanup(
		context.Background(), client, docker, slug, activeDigest,
		homeBySlugDigest, msbImagesBySlug, false, ui, report,
	)

	if report.PrunedVolumes != 1 {
		t.Errorf("PrunedVolumes = %d, want 1", report.PrunedVolumes)
	}
	if report.PrunedMSBImages != 1 {
		t.Errorf("PrunedMSBImages = %d, want 1", report.PrunedMSBImages)
	}
	if report.PrunedDockerImages != 1 {
		t.Errorf("PrunedDockerImages = %d, want 1", report.PrunedDockerImages)
	}

	if len(client.removedVolumes) != 1 || client.removedVolumes[0] != "opencode-msb-home-myproject-digest1" {
		t.Errorf("removed volumes = %v, want [opencode-msb-home-myproject-digest1]", client.removedVolumes)
	}

	if len(client.removedImages) != 1 || client.removedImages[0].ref != "opencode-msb/runner-myproject:digest1" {
		t.Errorf("removed msb images = %v, want [opencode-msb/runner-myproject:digest1]", client.removedImages)
	}

	if len(docker.removedImages) != 1 || docker.removedImages[0] != "opencode-msb/runner-myproject:digest1" {
		t.Errorf("removed docker images = %v, want [opencode-msb/runner-myproject:digest1]", docker.removedImages)
	}
}

func TestPruneActiveVMCleanup_DryRunCountsButDoesNotDelete(t *testing.T) {
	client := &mockMsbClient{}
	docker := &mockDockerClient{}
	ui := newMockUI()
	report := &StaleReport{}

	slug := "myproject"
	homeBySlugDigest := map[string]map[string]string{
		slug: {
			"digest1": "opencode-msb-home-myproject-digest1",
			"digest2": "opencode-msb-home-myproject-digest2",
		},
	}
	msbImagesBySlug := map[string][]imageWithDigest{
		slug: {
			{ref: "opencode-msb/runner-myproject:digest1", digest: "digest1", isLatest: false},
			{ref: "opencode-msb/runner-myproject:digest2", digest: "digest2", isLatest: false},
		},
	}

	pruneActiveVMCleanup(
		context.Background(), client, docker, slug, "digest2",
		homeBySlugDigest, msbImagesBySlug, true, ui, report,
	)

	if report.PrunedVolumes != 1 || report.PrunedMSBImages != 1 || report.PrunedDockerImages != 1 {
		t.Errorf("unexpected report counts: volumes=%d msb=%d docker=%d",
			report.PrunedVolumes, report.PrunedMSBImages, report.PrunedDockerImages)
	}
	if len(client.removedVolumes)+len(client.removedImages)+len(docker.removedImages) != 0 {
		t.Error("expected no deletion calls in dry run")
	}
}

func TestPruneOrphanSlug_RemovesEverything(t *testing.T) {
	client := &mockMsbClient{}
	docker := &mockDockerClient{}
	ui := newMockUI()
	report := &StaleReport{}

	slug := "orphan"
	homeBySlugDigest := map[string]map[string]string{
		slug: {
			"digest1": "opencode-msb-home-orphan-digest1",
			"":        "opencode-msb-home-orphan",
		},
	}
	msbImagesBySlug := map[string][]imageWithDigest{
		slug: {
			{ref: "opencode-msb/runner-orphan:digest1", digest: "digest1", isLatest: false},
			{ref: "opencode-msb/runner-orphan:latest", digest: "", isLatest: true},
		},
	}

	pruneOrphanSlug(context.Background(), client, docker, slug, homeBySlugDigest, msbImagesBySlug, report, false, ui)

	if report.PrunedVolumes != 2 {
		t.Errorf("PrunedVolumes = %d, want 2", report.PrunedVolumes)
	}
	if report.PrunedMSBImages != 2 {
		t.Errorf("PrunedMSBImages = %d, want 2", report.PrunedMSBImages)
	}
	if report.PrunedDockerImages != 2 {
		t.Errorf("PrunedDockerImages = %d, want 2", report.PrunedDockerImages)
	}

	if len(client.removedVolumes) != 2 {
		t.Errorf("removed volumes = %v, want 2", client.removedVolumes)
	}
	if len(client.removedImages) != 2 {
		t.Errorf("removed msb images = %v, want 2", client.removedImages)
	}
	if len(docker.removedImages) != 2 {
		t.Errorf("removed docker images = %v, want 2", docker.removedImages)
	}
}

func TestPruneCloneVolume_RemovesWhenNoActiveVM(t *testing.T) {
	client := &mockMsbClient{}
	ui := newMockUI()
	report := &StaleReport{}

	activeVMDigests := map[string]string{}
	cv := "opencode-msb-clone-myproject-abc123"

	pruneCloneVolume(context.Background(), client, cv, nil, activeVMDigests, false, ui, report)

	if report.PrunedCloneVolumes != 1 {
		t.Errorf("PrunedCloneVolumes = %d, want 1", report.PrunedCloneVolumes)
	}
	if len(client.removedVolumes) != 1 || client.removedVolumes[0] != cv {
		t.Errorf("removed volumes = %v, want [%s]", client.removedVolumes, cv)
	}
}

func TestPruneCloneVolume_KeepsWhenActiveVMExists(t *testing.T) {
	client := &mockMsbClient{}
	ui := newMockUI()
	report := &StaleReport{}

	activeVMDigests := map[string]string{"myproject": "digest1"}
	cv := "opencode-msb-clone-myproject-abc123"

	pruneCloneVolume(context.Background(), client, cv, nil, activeVMDigests, false, ui, report)

	if report.PrunedCloneVolumes != 0 {
		t.Errorf("PrunedCloneVolumes = %d, want 0", report.PrunedCloneVolumes)
	}
	if len(client.removedVolumes) != 0 {
		t.Errorf("expected no volume removal, got %v", client.removedVolumes)
	}
}

func TestPruneCloneVolume_DryRunDoesNotDelete(t *testing.T) {
	client := &mockMsbClient{}
	ui := newMockUI()
	report := &StaleReport{}

	cv := "opencode-msb-clone-myproject-abc123"
	pruneCloneVolume(context.Background(), client, cv, nil, map[string]string{}, true, ui, report)

	if report.PrunedCloneVolumes != 1 {
		t.Errorf("PrunedCloneVolumes = %d, want 1", report.PrunedCloneVolumes)
	}
	if len(client.removedVolumes) != 0 {
		t.Error("expected no deletion in dry run")
	}
}

func TestPrune_WithMocks_CoversAllCases(t *testing.T) {
	oldTime := time.Now().Add(-2 * time.Hour)
	recentTime := time.Now().Add(-5 * time.Minute)

	client := &mockMsbClient{
		sandboxes: []msbSandboxHandle{
			// Stale VM for myproject → cascade removes everything.
			mockSandboxHandle{
				name:      "opencode-msb-vm-myproject-1mjusbm3wikhb0",
				status:    msb.SandboxStatusStopped,
				updatedAt: oldTime,
			},
			// Active VM for activeproject → cleanup non-matching digests.
			mockSandboxHandle{
				name:      "opencode-msb-vm-activeproject-1mjusbm3wikhb0-main",
				status:    msb.SandboxStatusRunning,
				updatedAt: recentTime,
				image:     "opencode-msb/runner-activeproject-1mjusbm3wikhb0:digest2",
			},
			// Task sandbox → always pruned.
			mockSandboxHandle{
				name:      "opencode-msb-task-fill-proj",
				status:    msb.SandboxStatusStopped,
				updatedAt: oldTime,
			},
		},
		volumes: []msbVolumeHandle{
			mockVolumeHandle{name: "opencode-msb-home-myproject-1mjusbm3wikhb0-digest1"},
			mockVolumeHandle{name: "opencode-msb-home-activeproject-1mjusbm3wikhb0-digest1"},
			mockVolumeHandle{name: "opencode-msb-home-activeproject-1mjusbm3wikhb0-digest2"},
			mockVolumeHandle{name: "opencode-msb-clone-myproject-1mjusbm3wikhb0-abc123"},
			mockVolumeHandle{name: "opencode-msb-clone-activeproject-1mjusbm3wikhb0-def456"},
		},
		images: []msbImageHandle{
			mockImageHandle{ref: "opencode-msb/runner-myproject-1mjusbm3wikhb0:digest1"},
			mockImageHandle{ref: "opencode-msb/runner-activeproject-1mjusbm3wikhb0:digest1"},
			mockImageHandle{ref: "opencode-msb/runner-activeproject-1mjusbm3wikhb0:digest2"},
			mockImageHandle{ref: "opencode-msb/runner-orphan:latest"},
		},
	}
	docker := &mockDockerClient{}
	ui := newMockUI()

	oldNewMsbClient := newMsbClient
	newMsbClient = func() msbClient { return client }
	defer func() { newMsbClient = oldNewMsbClient }()

	report, err := Prune(context.Background(), docker, 30*time.Minute, false, ui)
	if err != nil {
		t.Fatalf("Prune returned error: %v", err)
	}

	if report.PrunedVMs != 1 {
		t.Errorf("PrunedVMs = %d, want 1", report.PrunedVMs)
	}
	if report.PrunedTaskSandboxes != 1 {
		t.Errorf("PrunedTaskSandboxes = %d, want 1", report.PrunedTaskSandboxes)
	}
	if report.PrunedVolumes != 2 {
		t.Errorf("PrunedVolumes = %d, want 2 (1 stale home + 1 active mismatch)", report.PrunedVolumes)
	}
	if report.PrunedCloneVolumes != 1 {
		t.Errorf("PrunedCloneVolumes = %d, want 1", report.PrunedCloneVolumes)
	}
	if report.PrunedMSBImages != 3 {
		t.Errorf("PrunedMSBImages = %d, want 3 (1 stale + 1 active mismatch + 1 orphan)", report.PrunedMSBImages)
	}
	if report.PrunedDockerImages != 3 {
		t.Errorf("PrunedDockerImages = %d, want 3", report.PrunedDockerImages)
	}
}

func TestPruneActiveVMDockerImages_AllFail_LogWarnings(t *testing.T) {
	docker := &mockDockerClient{
		removeErr: errors.New("image does not exist"),
	}
	report := &StaleReport{}
	ui := newMockUI()

	// Two images: digest1 is not :latest and != digest2, latest is skipped.
	slug := "myproject"
	msbImagesBySlug := map[string][]imageWithDigest{
		slug: {
			{ref: "opencode-msb/runner-myproject:digest1", digest: "digest1", isLatest: false},
			{ref: "opencode-msb/runner-myproject:latest", digest: "", isLatest: true},
		},
	}

	pruneActiveVMDockerImages(context.Background(), docker, slug, "digest2", msbImagesBySlug, false, ui, report)

	// No images should be counted as pruned when Docker removal always fails.
	if report.PrunedDockerImages != 0 {
		t.Errorf("PrunedDockerImages = %d, want 0", report.PrunedDockerImages)
	}
	// No images should appear in the removed list (all calls failed).
	if len(docker.removedImages) != 0 {
		t.Errorf("removedImages = %v, want [] (all calls failed)", docker.removedImages)
	}
	// There should be a verbose message about the failed docker image.
	if len(ui.VerboseCalls) != 1 {
		t.Errorf("WarnCalls = %d, want 1, got %v", len(ui.WarnCalls), ui.WarnCalls)
	}
}

func TestPruneActiveVMDockerImages_PartialFailure(t *testing.T) {
	docker := &mockDockerClient{
		// First call succeeds, second call fails (if a second call is made).
		perCallErrs: []error{
			nil,
			errors.New("image does not exist"),
		},
	}
	report := &StaleReport{}
	ui := newMockUI()

	// Two images: digest1 is not :latest and != digest2, latest is skipped.
	slug := "myproject"
	msbImagesBySlug := map[string][]imageWithDigest{
		slug: {
			{ref: "opencode-msb/runner-myproject:digest1", digest: "digest1", isLatest: false},
			{ref: "opencode-msb/runner-myproject:latest", digest: "", isLatest: true},
		},
	}

	pruneActiveVMDockerImages(context.Background(), docker, slug, "digest2", msbImagesBySlug, false, ui, report)

	// The first (and only non-latest) image should be pruned successfully.
	if report.PrunedDockerImages != 1 {
		t.Errorf("PrunedDockerImages = %d, want 1", report.PrunedDockerImages)
	}
	// Only the first image should be in the removed list.
	if len(docker.removedImages) != 1 || docker.removedImages[0] != "opencode-msb/runner-myproject:digest1" {
		t.Errorf("removedImages = %v, want [opencode-msb/runner-myproject:digest1]", docker.removedImages)
	}
}

// TestPruneStaleCascade_DockerRemoveFails verifies that when Docker image removal
// fails during a stale VM cascade, MSB image removal and volume removal still
// succeed, and the docker failure only affects the docker pruned count.
func TestPruneStaleCascade_DockerRemoveFails_DependentOpsSucceed(t *testing.T) {
	client := &mockMsbClient{}
	docker := &mockDockerClient{
		// First Docker removal succeeds, second fails.
		perCallErrs: []error{nil, errors.New("image not found")},
	}
	ui := newMockUI()
	report := &StaleReport{}

	slug := "myproject"
	entry := StaleEntry{Name: "opencode-msb-vm-myproject-abc123", Slug: slug}
	homeBySlugDigest := map[string]map[string]string{
		slug: {"digest1": "opencode-msb-home-myproject-digest1"},
	}
	msbImagesBySlug := map[string][]imageWithDigest{
		slug: {
			{ref: "opencode-msb/runner-myproject:digest1", digest: "digest1", isLatest: false},
			{ref: "opencode-msb/runner-myproject:latest", digest: "", isLatest: true},
		},
	}

	pruneStaleCascade(context.Background(), client, docker, entry, homeBySlugDigest, msbImagesBySlug, false, ui, report)

	// VM is pruned.
	if report.PrunedVMs != 1 {
		t.Errorf("PrunedVMs = %d, want 1", report.PrunedVMs)
	}
	// Home volumes are removed.
	if report.PrunedVolumes != 1 {
		t.Errorf("PrunedVolumes = %d, want 1", report.PrunedVolumes)
	}
	// Both MSB images are removed.
	if report.PrunedMSBImages != 2 {
		t.Errorf("PrunedMSBImages = %d, want 2", report.PrunedMSBImages)
	}
	// Docker: first image succeeds, second fails → only 1 pruned.
	if report.PrunedDockerImages != 1 {
		t.Errorf("PrunedDockerImages = %d, want 1 (partial docker failure)", report.PrunedDockerImages)
	}
	// Verify MSB removals.
	if len(client.removedVolumes) != 1 {
		t.Errorf("removed volumes = %v, want [opencode-msb-home-myproject-digest1]", client.removedVolumes)
	}
	if len(client.removedImages) != 2 {
		t.Errorf("removed MSB images count = %d, want 2", len(client.removedImages))
	}
	// Docker: only the first image removed (second fails, doesn't count).
	if len(docker.removedImages) != 1 {
		t.Errorf("removed docker images = %v, want 1", docker.removedImages)
	}
	// There should be a verbose message about the failed docker image.
	if len(ui.VerboseCalls) != 1 {
		t.Errorf("WarnCalls = %d, want 1, got %v", len(ui.WarnCalls), ui.WarnCalls)
	}
}

func TestPrune_DockerRemoveFails_PartialReport(t *testing.T) {
	oldTime := time.Now().Add(-2 * time.Hour)

	client := &mockMsbClient{
		sandboxes: []msbSandboxHandle{
			// Stale VM for myproject → cascade removes everything.
			mockSandboxHandle{
				name:      "opencode-msb-vm-myproject-1mjusbm3wikhb0",
				status:    msb.SandboxStatusStopped,
				updatedAt: oldTime,
			},
		},
		volumes: []msbVolumeHandle{
			mockVolumeHandle{name: "opencode-msb-home-myproject-1mjusbm3wikhb0-digest1"},
		},
		images: []msbImageHandle{
			mockImageHandle{ref: "opencode-msb/runner-myproject-1mjusbm3wikhb0:digest1"},
			mockImageHandle{ref: "opencode-msb/runner-myproject-1mjusbm3wikhb0:latest"},
		},
	}
	docker := &mockDockerClient{
		// First succeeds, second fails.
		perCallErrs: []error{nil, errors.New("image not found")},
	}
	ui := newMockUI()

	oldNewMsbClient := newMsbClient
	newMsbClient = func() msbClient { return client }
	defer func() { newMsbClient = oldNewMsbClient }()

	report, err := Prune(context.Background(), docker, 30*time.Minute, false, ui)
	if err != nil {
		t.Fatalf("Prune returned error: %v", err)
	}

	// 1 stale VM is pruned.
	if report.PrunedVMs != 1 {
		t.Errorf("PrunedVMs = %d, want 1", report.PrunedVMs)
	}
	// Stale cascade removes all home volumes.
	if report.PrunedVolumes != 1 {
		t.Errorf("PrunedVolumes = %d, want 1", report.PrunedVolumes)
	}
	// Both MSB images are removed by cascade.
	if report.PrunedMSBImages != 2 {
		t.Errorf("PrunedMSBImages = %d, want 2", report.PrunedMSBImages)
	}
	// Docker: first image succeeds, second fails → only 1 pruned.
	if report.PrunedDockerImages != 1 {
		t.Errorf("PrunedDockerImages = %d, want 1 (partial docker failure)", report.PrunedDockerImages)
	}
	// There should be a verbose message about the failed docker image.
	if len(ui.VerboseCalls) != 1 {
		t.Errorf("WarnCalls = %d, want 1, got %v", len(ui.WarnCalls), ui.WarnCalls)
	}
}
