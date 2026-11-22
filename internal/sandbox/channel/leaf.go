package channel

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"sync"
	"time"

	"github.com/elazarl/goproxy"
)

// mitmTLSConfig returns goproxy's per-CONNECT TLS config. The terminating
// certificate is issued for the name the client verifies: the SNI of its
// ClientHello, or the CONNECT target when it sends none. The msb runtime opens
// the tunnel by IP and verifies the certificate against the hostname it sends
// as SNI, so a certificate for the CONNECT target alone fails its handshake.
func mitmTLSConfig(ca tls.Certificate) func(host string, _ *goproxy.ProxyCtx) (*tls.Config, error) {
	issuer := newLeafIssuer(ca)
	return func(host string, _ *goproxy.ProxyCtx) (*tls.Config, error) {
		connectHost, _ := splitHostPort(host)
		//nolint:exhaustruct_v5 // a terminating server config sets only the certificate hook and the version floor
		return &tls.Config{
			MinVersion: tls.VersionTLS12,
			GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
				name := hello.ServerName
				if name == "" {
					name = connectHost
				}
				return issuer.leafFor(name)
			},
		}, nil
	}
}

// leafIssuer signs one terminating certificate per name with the session CA
// and reuses it for every later connection to that name.
type leafIssuer struct {
	ca tls.Certificate

	mu     sync.Mutex
	leaves map[string]*tls.Certificate
}

func newLeafIssuer(ca tls.Certificate) *leafIssuer {
	return &leafIssuer{ca: ca, mu: sync.Mutex{}, leaves: map[string]*tls.Certificate{}}
}

func (i *leafIssuer) leafFor(name string) (*tls.Certificate, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if leaf, ok := i.leaves[name]; ok {
		return leaf, nil
	}
	leaf, err := signLeaf(i.ca, name)
	if err != nil {
		return nil, err
	}
	i.leaves[name] = leaf
	return leaf, nil
}

// signLeaf issues a server certificate for name, as an IP SAN when name is an
// IP literal and as a DNS SAN otherwise. Its lifetime matches the session CA.
func signLeaf(ca tls.Certificate, name string) (*tls.Certificate, error) {
	key, err := generateECKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	//nolint:exhaustruct_v5 // a session leaf sets only the fields it needs; the rest stay zero
	template := &x509.Certificate{
		SerialNumber: big.NewInt(now.UnixNano()),
		Subject:      pkix.Name{CommonName: name},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     ca.Leaf.NotAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if ip := net.ParseIP(name); ip != nil {
		template.IPAddresses = []net.IP{ip}
	} else {
		template.DNSNames = []string{name}
	}
	caKey, _ := ca.PrivateKey.(*ecdsa.PrivateKey)
	der, err := createCertificate(rand.Reader, template, ca.Leaf, &key.PublicKey, caKey)
	if err != nil {
		return nil, err
	}
	//nolint:exhaustruct_v5 // the chain and key are all a server certificate needs; tls parses the leaf on demand
	return &tls.Certificate{Certificate: [][]byte{der, ca.Certificate[0]}, PrivateKey: key}, nil
}
