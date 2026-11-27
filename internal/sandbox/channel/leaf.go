package channel

import (
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
// onServerName learns the SNI and session each handshake presents for its
// CONNECT target.
// Session tickets are off so every handshake reaches GetCertificate: a resumed
// session would skip it and leave its tunnel unnamed.
func mitmTLSConfig(
	ca tls.Certificate,
	onServerName func(connectTarget, serverName string, sessionID uint16),
) func(host string, ctx *goproxy.ProxyCtx) (*tls.Config, error) {
	issuer := newLeafIssuer(ca)
	return func(host string, ctx *goproxy.ProxyCtx) (*tls.Config, error) {
		connectHost, _ := splitHostPort(host)
		sessionID := proxySessionID(ctx.Session)
		//nolint:exhaustruct_v5 // a terminating server config sets only the certificate hook, tickets, and the version floor
		return &tls.Config{
			MinVersion:             tls.VersionTLS12,
			SessionTicketsDisabled: true,
			GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
				name := hello.ServerName
				if name == "" {
					name = connectHost
				} else {
					onServerName(host, name, sessionID)
				}
				return issuer.leafFor(name)
			},
		}, nil
	}
}

// maxCachedLeaves bounds the leaf cache: the names come from the guest's SNI,
// so a session cannot grow host memory without limit. A name past the bound is
// signed per handshake instead.
const maxCachedLeaves = 1024

// maxLeafSerial bounds a leaf's random serial number to 64 bits.
var maxLeafSerial = new(big.Int).Lsh(big.NewInt(1), 64)

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
	if len(i.leaves) < maxCachedLeaves {
		i.leaves[name] = leaf
	}
	return leaf, nil
}

// signLeaf issues a server certificate for name, as an IP SAN when name is an
// IP literal and as a DNS SAN otherwise. Its lifetime matches the session CA.
func signLeaf(ca tls.Certificate, name string) (*tls.Certificate, error) {
	key, err := generateECKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := randomSerial(rand.Reader, maxLeafSerial)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	//nolint:exhaustruct_v5 // a session leaf sets only the fields it needs; the rest stay zero
	template := &x509.Certificate{
		SerialNumber: serial,
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
	der, err := createCertificate(rand.Reader, template, ca.Leaf, &key.PublicKey, ca.PrivateKey)
	if err != nil {
		return nil, err
	}
	//nolint:exhaustruct_v5 // the chain and key are all a server certificate needs; tls parses the leaf on demand
	return &tls.Certificate{Certificate: [][]byte{der, ca.Certificate[0]}, PrivateKey: key}, nil
}
