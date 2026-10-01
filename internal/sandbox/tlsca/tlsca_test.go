package tlsca

import (
	"bytes"
	"crypto/ecdsa"
	cryptorand "crypto/rand"
	"crypto/x509"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
)

func TestEnsureGeneratesAndPersistsCA(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ca, err := Ensure()
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	for _, path := range []string{ca.CertPath, ca.KeyPath} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected CA file %s to exist: %v", path, err)
		}
	}
	cert, err := ca.Cert()
	if err != nil {
		t.Fatalf("Cert: %v", err)
	}
	if !strings.Contains(string(cert), "BEGIN CERTIFICATE") {
		t.Error("CA cert must be PEM encoded")
	}
	if fp, err := ca.Fingerprint(); err != nil {
		t.Errorf("Fingerprint: %v", err)
	} else if len(fp) != 64 {
		t.Errorf("Fingerprint length = %d, want 64 hex chars", len(fp))
	}
}

func TestEnsureIsIdempotent(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	first, err := Ensure()
	if err != nil {
		t.Fatalf("first Ensure: %v", err)
	}
	firstCert, _ := first.Cert()
	second, err := Ensure()
	if err != nil {
		t.Fatalf("second Ensure: %v", err)
	}
	secondCert, _ := second.Cert()
	if string(firstCert) != string(secondCert) {
		t.Error("Ensure must reuse the persisted CA rather than regenerate it")
	}
}

func TestKeyFilePermissions(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ca, err := Ensure()
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	info, err := os.Stat(ca.KeyPath)
	if err != nil {
		t.Fatalf("stat key: %v", err)
	}
	if mode := info.Mode().Perm(); mode != keyMode {
		t.Errorf("key mode = %v, want %v", mode, keyMode)
	}
	info, err = os.Stat(ca.CertPath)
	if err != nil {
		t.Fatalf("stat cert: %v", err)
	}
	if mode := info.Mode().Perm(); mode != certMode {
		t.Errorf("cert mode = %v, want %v", mode, certMode)
	}
}

func TestLoadErrorsBeforeGeneration(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	if _, err := Load(); err == nil {
		t.Fatal("Load before Ensure must fail")
	}
}

func TestCertFingerprint(t *testing.T) {
	first := CertFingerprint([]byte("abc"))
	second := CertFingerprint([]byte("abc"))
	third := CertFingerprint([]byte("abd"))
	if first != second {
		t.Error("CertFingerprint must be deterministic")
	}
	if first == third {
		t.Error("CertFingerprint must differ for different content")
	}
}

func TestEnsureCAFilesPlacedUnderStateDir(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	ca, err := Ensure()
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	stateDir := configpaths.Get().UserStateDir()
	for _, path := range []string{ca.CertPath, ca.KeyPath} {
		if !strings.HasPrefix(path, stateDir) {
			t.Errorf("CA file %s must live under the user state dir %s", path, stateDir)
		}
	}
	if filepath.Dir(ca.CertPath) != filepath.Join(stateDir, "tls") {
		t.Errorf("CA dir = %s, want %s", filepath.Dir(ca.CertPath), filepath.Join(stateDir, "tls"))
	}
}

func TestFingerprintErrorsOnMissingCert(t *testing.T) {
	ca := &CA{CertPath: filepath.Join(t.TempDir(), "missing.crt"), KeyPath: "unused"}
	if _, err := ca.Fingerprint(); err == nil {
		t.Fatal("Fingerprint must fail when the CA certificate file is missing")
	}
}

func TestEnsureErrorWhenTLSDirBlockedByFile(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	blocked := filepath.Join(configpaths.Get().UserStateDir(), "tls")
	if err := os.WriteFile(blocked, []byte("not a dir"), 0o644); err != nil {
		t.Fatalf("write blocking file: %v", err)
	}
	if _, err := Ensure(); err == nil {
		t.Fatal("Ensure must fail when the TLS dir cannot be created")
	}
}

func TestEnsureErrorWhenLockFileIsDirectory(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	tlsPath := filepath.Join(configpaths.Get().UserStateDir(), "tls")
	if err := os.MkdirAll(tlsPath, dirMode); err != nil {
		t.Fatalf("create TLS dir: %v", err)
	}
	if err := os.Mkdir(filepath.Join(tlsPath, lockFileName), 0o700); err != nil {
		t.Fatalf("create blocking lock dir: %v", err)
	}
	if _, err := Ensure(); err == nil {
		t.Fatal("Ensure must fail when the lock file cannot be opened")
	}
}

func TestEnsureConcurrentCallsReturnSameCA(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	const callers = 8
	var wg sync.WaitGroup
	cas := make([]*CA, callers)
	errs := make([]error, callers)
	for i := range callers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cas[i], errs[i] = Ensure()
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("Ensure (caller %d): %v", i, err)
		}
	}
	first, err := cas[0].Cert()
	if err != nil {
		t.Fatalf("read first CA cert: %v", err)
	}
	for i := 1; i < callers; i++ {
		other, err := cas[i].Cert()
		if err != nil {
			t.Fatalf("read CA cert %d: %v", i, err)
		}
		if !bytes.Equal(first, other) {
			t.Errorf("concurrent Ensure calls returned different CAs (caller %d)", i)
		}
	}
}

// failingReader is an io.Reader that always errors, for exercising the
// crypto/rand failure paths of generate.
type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestGenerateErrorOnRandomFailure(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	tlsPath := filepath.Join(configpaths.Get().UserStateDir(), "tls")
	if err := os.MkdirAll(tlsPath, dirMode); err != nil {
		t.Fatalf("create TLS dir: %v", err)
	}
	orig := cryptorand.Reader
	cryptorand.Reader = failingReader{}
	t.Cleanup(func() { cryptorand.Reader = orig })
	if _, err := generate(); err == nil {
		t.Fatal("generate must fail when the crypto/rand source errors")
	}
}

func TestAcquireLockErrorOnFlockFailure(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	tlsPath := filepath.Join(configpaths.Get().UserStateDir(), "tls")
	if err := os.MkdirAll(tlsPath, dirMode); err != nil {
		t.Fatalf("create TLS dir: %v", err)
	}
	ops := realCAOps()
	ops.lockExclusive = func(*os.File) error { return errors.New("flock denied") }
	if _, err := acquireLockWith(ops); err == nil {
		t.Fatal("acquireLock must fail when flock fails")
	}
}

func TestGenerateErrorOnKeyGeneration(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	tlsPath := filepath.Join(configpaths.Get().UserStateDir(), "tls")
	if err := os.MkdirAll(tlsPath, dirMode); err != nil {
		t.Fatalf("create TLS dir: %v", err)
	}
	ops := realCAOps()
	ops.generateKey = func() (*ecdsa.PrivateKey, error) { return nil, errors.New("key gen denied") }
	if _, err := generateWith(ops); err == nil {
		t.Fatal("generate must fail when CA key generation fails")
	}
}

func TestGenerateErrorOnCertificateCreation(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	tlsPath := filepath.Join(configpaths.Get().UserStateDir(), "tls")
	if err := os.MkdirAll(tlsPath, dirMode); err != nil {
		t.Fatalf("create TLS dir: %v", err)
	}
	ops := realCAOps()
	ops.createCert = func(*x509.Certificate, *x509.Certificate, any, any) ([]byte, error) {
		return nil, errors.New("cert creation denied")
	}
	if _, err := generateWith(ops); err == nil {
		t.Fatal("generate must fail when certificate creation fails")
	}
}

func TestGenerateErrorOnKeyMarshal(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	tlsPath := filepath.Join(configpaths.Get().UserStateDir(), "tls")
	if err := os.MkdirAll(tlsPath, dirMode); err != nil {
		t.Fatalf("create TLS dir: %v", err)
	}
	ops := realCAOps()
	ops.marshalKey = func(*ecdsa.PrivateKey) ([]byte, error) { return nil, errors.New("key marshal denied") }
	if _, err := generateWith(ops); err == nil {
		t.Fatal("generate must fail when CA key marshaling fails")
	}
}

func TestGenerateErrorWhenCertPathIsDirectory(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	tlsPath := filepath.Join(configpaths.Get().UserStateDir(), "tls")
	if err := os.MkdirAll(tlsPath, dirMode); err != nil {
		t.Fatalf("create TLS dir: %v", err)
	}
	if err := os.Mkdir(filepath.Join(tlsPath, caFileName), 0o700); err != nil {
		t.Fatalf("create blocking cert dir: %v", err)
	}
	if _, err := generate(); err == nil {
		t.Fatal("generate must fail when the cert path is blocked by a directory")
	}
}

func TestGenerateErrorWhenKeyPathIsDirectory(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	tlsPath := filepath.Join(configpaths.Get().UserStateDir(), "tls")
	if err := os.MkdirAll(tlsPath, dirMode); err != nil {
		t.Fatalf("create TLS dir: %v", err)
	}
	if err := os.Mkdir(filepath.Join(tlsPath, keyFileName), 0o700); err != nil {
		t.Fatalf("create blocking key dir: %v", err)
	}
	if _, err := generate(); err == nil {
		t.Fatal("generate must fail when the key path is blocked by a directory")
	}
}

func TestAtomicWriteErrorOnTempPathIsDirectory(t *testing.T) {
	target := filepath.Join(t.TempDir(), "ca.key")
	if err := os.Mkdir(target+".tmp", 0o700); err != nil {
		t.Fatalf("create blocking temp dir: %v", err)
	}
	if err := atomicWrite(target, []byte("data"), 0o600); err == nil {
		t.Fatal("atomicWrite must fail when the temp path is a directory")
	}
}

func TestAtomicWriteErrorOnTargetIsDirectory(t *testing.T) {
	target := filepath.Join(t.TempDir(), "ca.crt")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatalf("create blocking target dir: %v", err)
	}
	if err := atomicWrite(target, []byte("data"), 0o644); err == nil {
		t.Fatal("atomicWrite must fail when the target is a directory")
	}
}
