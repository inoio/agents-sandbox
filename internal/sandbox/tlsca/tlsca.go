// Package tlsca manages the host-side TLS interception CA used to configure
// the microsandbox transparent HTTPS proxy. The CA keypair is generated once
// per machine and persisted under the user state directory; only the
// certificate is ever shipped into runner images or guests.
package tlsca

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
)

// CA names the persisted TLS interception CA keypair.
type CA struct {
	CertPath string
	KeyPath  string
}

const (
	caFileName   = "ca.crt"
	keyFileName  = "ca.key"
	lockFileName = "lock"

	dirMode  = 0o700
	keyMode  = 0o600
	certMode = 0o644
)

// caOps bundles the failure-prone crypto and lock primitives used while
// generating and locking the CA. Production code uses the real operations;
// tests inject failing replacements to exercise the error branches, which
// cannot be provoked with valid inputs (Go's NIST-curve key generation and
// signing draw from an internal RNG, and flock succeeds on local filesystems).
type caOps struct {
	generateKey   func() (*ecdsa.PrivateKey, error)
	createCert    func(template, parent *x509.Certificate, publicKey, privateKey any) ([]byte, error)
	marshalKey    func(key *ecdsa.PrivateKey) ([]byte, error)
	lockExclusive func(f *os.File) error
}

// realCAOps returns the production implementations for the CA operations.
func realCAOps() caOps {
	return caOps{
		generateKey: func() (*ecdsa.PrivateKey, error) {
			return ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		},
		createCert: func(template, parent *x509.Certificate, publicKey, privateKey any) ([]byte, error) {
			return x509.CreateCertificate(rand.Reader, template, parent, publicKey, privateKey)
		},
		marshalKey:    x509.MarshalECPrivateKey,
		lockExclusive: state.FlockExclusive,
	}
}

// Cert returns the CA certificate PEM bytes.
func (c *CA) Cert() ([]byte, error) {
	return os.ReadFile(c.CertPath)
}

// Fingerprint returns a SHA-256 hex digest of the CA certificate, used to
// detect CA changes that require recreating VMs and rebuilding runner images.
func (c *CA) Fingerprint() (string, error) {
	data, err := c.Cert()
	if err != nil {
		return "", err
	}
	return CertFingerprint(data), nil
}

// CertFingerprint returns the SHA-256 hex digest of a certificate PEM blob.
func CertFingerprint(pemBytes []byte) string {
	sum := sha256.Sum256(pemBytes)
	return hex.EncodeToString(sum[:])
}

// Ensure returns the host's TLS interception CA, generating and persisting a
// fresh self-signed keypair on first use. It is idempotent and safe to call
// from concurrent processes.
func Ensure() (*CA, error) {
	if ca, err := Load(); err == nil {
		return ca, nil
	}
	unlock, err := acquireLock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	if ca, err := Load(); err == nil {
		return ca, nil
	}
	return generate()
}

// Load returns the persisted CA, or an error when it does not exist yet.
func Load() (*CA, error) {
	ca := &CA{
		CertPath: filepath.Join(tlsDir(), caFileName),
		KeyPath:  filepath.Join(tlsDir(), keyFileName),
	}
	for _, path := range []string{ca.CertPath, ca.KeyPath} {
		if _, err := os.Stat(path); err != nil {
			return nil, fmt.Errorf("load TLS interception CA: %w", err)
		}
	}
	return ca, nil
}

func tlsDir() string {
	return filepath.Join(configpaths.Get().UserStateDir(), "tls")
}

// acquireLock takes an exclusive advisory lock on the CA directory so that
// concurrent Ensure calls do not generate two different keypairs.
func acquireLock() (func(), error) {
	return acquireLockWith(realCAOps())
}

func acquireLockWith(ops caOps) (func(), error) {
	dir := tlsDir()
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return nil, fmt.Errorf("create TLS CA dir %s: %w", dir, err)
	}
	f, err := os.OpenFile(filepath.Join(dir, lockFileName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open TLS CA lock: %w", err)
	}
	if err := ops.lockExclusive(f); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("lock TLS CA dir: %w", err)
	}
	return func() { _ = f.Close() }, nil
}

// generate creates a new self-signed EC P-256 CA and persists it atomically.
func generate() (*CA, error) {
	return generateWith(realCAOps())
}

func generateWith(ops caOps) (*CA, error) {
	ca := &CA{
		CertPath: filepath.Join(tlsDir(), caFileName),
		KeyPath:  filepath.Join(tlsDir(), keyFileName),
	}
	key, err := ops.generateKey()
	if err != nil {
		return nil, fmt.Errorf("generate TLS interception CA key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("generate TLS interception CA serial: %w", err)
	}
	template := &x509.Certificate{ //nolint:exhaustruct // only CA fields are relevant
		SerialNumber: serial,
		Subject: pkix.Name{ //nolint:exhaustruct // only CN/O are needed
			CommonName:   "agents-sandbox CA",
			Organization: []string{"agents-sandbox"},
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := ops.createCert(template, template, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("create TLS interception CA certificate: %w", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}) //nolint:exhaustruct // headers not used
	keyDER, err := ops.marshalKey(key)
	if err != nil {
		return nil, fmt.Errorf("encode TLS interception CA key: %w", err)
	}
	keyBlock := &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER} //nolint:exhaustruct // headers not used
	keyPEM := pem.EncodeToMemory(keyBlock)
	if err := atomicWrite(ca.CertPath, certPEM, certMode); err != nil {
		return nil, err
	}
	if err := atomicWrite(ca.KeyPath, keyPEM, keyMode); err != nil {
		return nil, err
	}
	return ca, nil
}

// atomicWrite writes data to path via a temp file and rename, so a crash never
// leaves a partial keypair behind.
func atomicWrite(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename %s: %w", path, err)
	}
	return nil
}
