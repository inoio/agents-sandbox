package journeys

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"time"
)

// stubCAHomePath is where a journey provisions the stub's test CA in the VM
// home, through the launcher's home: manifest; stubCAGuestPath is the same
// file as the harness sees it.
const (
	stubCAHomePath  = ".journeys/stub-ca.pem"
	stubCAGuestPath = "/home/dev/" + stubCAHomePath
)

// stubCAEnv names the variable Node and Bun read extra trusted CAs from, so a
// harness built on either trusts the stub's test CA. It replaces any other
// value, such as the CA msb's TLS interception sets. A secret-substitution
// journey therefore points it at a bundle of the runtime and stub CAs.
const stubCAEnv = "NODE_EXTRA_CA_CERTS"

// stubTLS is a test CA and a server certificate it issued for the VM's name
// for the host, which the VM and the fake browser both reach the stub under.
type stubTLS struct {
	caPEM  []byte
	pool   *x509.CertPool
	server tls.Certificate
}

// newStubTLS issues a fresh, short-lived test CA and server certificate.
func newStubTLS() (*stubTLS, error) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	// gomutants:disable-next-line BRANCH_IF reason="key generation fails only on a broken system random source, which a test cannot provoke"
	if err != nil {
		return nil, fmt.Errorf("generate the test CA key: %w", err)
	}
	now := time.Now()
	caTemplate := &x509.Certificate{
		// gomutants:disable-next-line INTEGER_INCREMENT,INTEGER_DECREMENT reason="a test CA's serial is no contract; any value the certificate parser accepts serves"
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "agents-sandbox journeys test CA"},
		NotBefore:    now.Add(-time.Hour),
		// gomutants:disable-next-line INTEGER_INCREMENT,INTEGER_DECREMENT reason="the validity only has to outlast a suite run; its exact length is no contract"
		NotAfter:              now.Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	// gomutants:disable-next-line BRANCH_IF reason="the constant template always issues; only a broken random source fails here"
	if err != nil {
		return nil, fmt.Errorf("issue the test CA: %w", err)
	}
	ca, err := x509.ParseCertificate(caDER)
	// gomutants:disable-next-line BRANCH_IF reason="parsing the certificate CreateCertificate just produced cannot fail"
	if err != nil {
		return nil, fmt.Errorf("parse the test CA: %w", err)
	}
	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	// gomutants:disable-next-line BRANCH_IF reason="key generation fails only on a broken system random source, which a test cannot provoke"
	if err != nil {
		return nil, fmt.Errorf("generate the server key: %w", err)
	}
	serverTemplate := &x509.Certificate{
		// gomutants:disable-next-line INTEGER_INCREMENT,INTEGER_DECREMENT reason="a test certificate's serial is no contract; any value the parser accepts serves"
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: stubGatewayHost},
		DNSNames:     []string{stubGatewayHost},
		NotBefore:    now.Add(-time.Hour),
		// gomutants:disable-next-line INTEGER_INCREMENT,INTEGER_DECREMENT reason="the validity only has to outlast a suite run; its exact length is no contract"
		NotAfter:    now.Add(24 * time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	serverDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, ca, &serverKey.PublicKey, caKey)
	// gomutants:disable-next-line BRANCH_IF reason="the constant template always issues; only a broken random source fails here"
	if err != nil {
		return nil, fmt.Errorf("issue the server certificate: %w", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	return &stubTLS{
		caPEM:  pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}),
		pool:   pool,
		server: tls.Certificate{Certificate: [][]byte{serverDER}, PrivateKey: serverKey},
	}, nil
}

// stubCA is the test CA the journey's model or OAuth stub serves HTTPS with, or
// nil. A journey that needs it starts that stub before it seeds the home and
// the launcher env, which provision the CA and point the harness at it.
func stubCA(c *Context) []byte {
	if c.Stub != nil && len(c.Stub.CAPEM()) != 0 {
		return c.Stub.CAPEM()
	}
	if c.OAuth == nil {
		return nil
	}
	return c.OAuth.CAPEM()
}
