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
	"net"
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
// harness built on either trusts the stub's test CA.
const stubCAEnv = "NODE_EXTRA_CA_CERTS"

// stubTLS is a test CA and a server certificate it issued for the names the
// stub is reached under: the VM's name for the host and the host loopback.
type stubTLS struct {
	caPEM  []byte
	pool   *x509.CertPool
	server tls.Certificate
}

// newStubTLS issues a fresh, short-lived test CA and server certificate.
func newStubTLS() (*stubTLS, error) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate the test CA key: %w", err)
	}
	now := time.Now()
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "agents-sandbox journeys test CA"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, fmt.Errorf("issue the test CA: %w", err)
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		return nil, fmt.Errorf("parse the test CA: %w", err)
	}
	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate the server key: %w", err)
	}
	serverTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: stubGatewayHost},
		DNSNames:     []string{stubGatewayHost},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	serverDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, ca, &serverKey.PublicKey, caKey)
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

// stubCA is the test CA the journey's stub OAuth server serves HTTPS with, or
// nil. A journey that needs it starts that stub before it seeds the home and
// the launcher env, which provision the CA and point the harness at it.
func stubCA(c *Context) []byte {
	if c.OAuth == nil {
		return nil
	}
	return c.OAuth.CAPEM()
}
