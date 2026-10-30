package image

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/agent"
)

// vendoredTrustHelperPath is the on-disk source of the trust helper asset. The
// test reads it directly so the assertion fails when the build context ships
// any other bytes, rather than comparing the asset against itself.
const vendoredTrustHelperPath = "data/msb-trust.sh"

// vendoredTrustHelperLicensePath records the provenance of the vendored helper,
// including the hash the tests hold it to.
const vendoredTrustHelperLicensePath = "data/msb-trust.LICENSE"

// TestWriteDockerfileTarShipsTrustHelperExecutable covers the synthetic build
// context the image build consumes. The final stage COPYs the helper into the
// image and takes the executable bit from the context, so the file has to
// travel as a regular file with mode 755; a context entry the COPY cannot
// resolve fails the whole build.
func TestWriteDockerfileTarShipsTrustHelperExecutable(t *testing.T) {
	var context bytes.Buffer
	if err := writeDockerfileTar(&context, []byte("FROM scratch\n")); err != nil {
		t.Fatalf("writeDockerfileTar: %v", err)
	}

	entries, modes := readTarEntries(t, &context)

	want, err := os.ReadFile(vendoredTrustHelperPath)
	if err != nil {
		t.Fatalf("read vendored trust helper: %v", err)
	}
	if got := entries[trustHelperAsset]; got != string(want) {
		t.Errorf("tar entry %q carries %d bytes, want the vendored script's %d", trustHelperAsset, len(got), len(want))
	}
	if got := modes[trustHelperAsset]; got != 0o755 {
		t.Errorf("tar entry %q mode = %o, want 755 so the installed command is executable", trustHelperAsset, got)
	}
	if got := modes[pathMergeAsset]; got != 0o644 {
		t.Errorf("tar entry %q mode = %o, want 644 (only the helper is a command)", pathMergeAsset, got)
	}
}

// TestRenderDockerfileInstallsTrustHelper covers the runner-stage wiring: a
// build-context asset changes nothing until the final stage copies it into the
// image, and a path outside the composed PATH would not be runnable by name.
func TestRenderDockerfileInstallsTrustHelper(t *testing.T) {
	a, ok := agent.Lookup("opencode")
	if !ok {
		t.Fatal("opencode agent not registered")
	}
	final := finalStage(t, mustRender(t, a, nil, dockerOn))
	if !strings.Contains(final, "COPY "+trustHelperAsset+" "+trustHelperDest) {
		t.Errorf("final stage must COPY %s to %s; got:\n%s", trustHelperAsset, trustHelperDest, final)
	}
}

// TestWriteDockerfileTarShipsTrustHelperLicense covers the license asset in the
// synthetic build context. The helper is upstream's Apache-2.0 work, so whoever
// receives it — a built runner image, or the release binary that embeds the same
// bytes — must also receive the license copy section 4(a) requires. The final
// stage COPYs the context entry, so it travels as a non-executable regular file.
func TestWriteDockerfileTarShipsTrustHelperLicense(t *testing.T) {
	var context bytes.Buffer
	if err := writeDockerfileTar(&context, []byte("FROM scratch\n")); err != nil {
		t.Fatalf("writeDockerfileTar: %v", err)
	}

	entries, modes := readTarEntries(t, &context)

	want, err := os.ReadFile(vendoredTrustHelperLicensePath)
	if err != nil {
		t.Fatalf("read vendored trust helper license: %v", err)
	}
	if got := entries[trustHelperLicenseAsset]; got != string(want) {
		t.Errorf(
			"tar entry %q carries %d bytes, want the vendored license's %d",
			trustHelperLicenseAsset,
			len(got),
			len(want),
		)
	}
	if got := modes[trustHelperLicenseAsset]; got != 0o644 {
		t.Errorf("tar entry %q mode = %o, want 644 (the license is not a command)", trustHelperLicenseAsset, got)
	}
}

// TestRenderDockerfileInstallsTrustHelperLicense covers the runner-stage wiring
// for the license. Shipping it in the build context changes nothing until the
// final stage installs it; a helper present without the license copy is exactly
// the packaging Apache-2.0 section 4(a) forbids.
func TestRenderDockerfileInstallsTrustHelperLicense(t *testing.T) {
	a, ok := agent.Lookup("opencode")
	if !ok {
		t.Fatal("opencode agent not registered")
	}
	final := finalStage(t, mustRender(t, a, nil, dockerOn))
	if !strings.Contains(final, "COPY "+trustHelperLicenseAsset+" "+trustHelperLicenseDest) {
		t.Errorf("final stage must COPY %s to %s; got:\n%s", trustHelperLicenseAsset, trustHelperLicenseDest, final)
	}
}

// readTarEntries returns the tar's entry contents and modes keyed by name.
func readTarEntries(t *testing.T, r io.Reader) (map[string]string, map[string]int64) {
	t.Helper()
	entries := map[string]string{}
	modes := map[string]int64{}
	tr := tar.NewReader(r)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return entries, modes
		}
		if err != nil {
			t.Fatalf("read tar: %v", err)
		}
		content, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("read tar entry %q: %v", header.Name, err)
		}
		entries[header.Name] = string(content)
		modes[header.Name] = header.Mode
	}
}

// TestVendoredTrustHelperMatchesRecordedHash enforces the verbatim-vendoring
// contract: the helper is upstream's file unmodified, and the hash beside it
// names the revision we shipped. Re-syncing the script has to update the
// recorded hash; hand-editing it — a tempting way to "fix" the messages that say
// `sh msb-trust.sh` — fails here instead of silently making the license lie.
func TestVendoredTrustHelperMatchesRecordedHash(t *testing.T) {
	script, err := os.ReadFile(vendoredTrustHelperPath)
	if err != nil {
		t.Fatalf("read vendored trust helper: %v", err)
	}
	license, err := os.ReadFile(vendoredTrustHelperLicensePath)
	if err != nil {
		t.Fatalf("read trust helper license: %v", err)
	}

	want := recordedTrustHelperHash(t, string(license))
	if got := fmt.Sprintf("%x", sha256.Sum256(script)); got != want {
		t.Errorf(
			"%s hashes to %s, but %s records %s",
			vendoredTrustHelperPath,
			got,
			vendoredTrustHelperLicensePath,
			want,
		)
	}
}

// recordedTrustHelperHash returns the SHA-256 recorded in the license file's
// provenance header.
func recordedTrustHelperHash(t *testing.T, license string) string {
	t.Helper()
	for line := range strings.SplitSeq(license, "\n") {
		hash, found := strings.CutPrefix(strings.TrimSpace(line), "SHA-256:")
		if found {
			return strings.TrimSpace(hash)
		}
	}
	t.Fatalf("%s records no SHA-256 line", vendoredTrustHelperLicensePath)
	return ""
}
