package channel

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"math/big"
	"net"
	"strconv"
	"testing"

	"github.com/elazarl/goproxy"
)

func testCA(t *testing.T) tls.Certificate {
	t.Helper()
	ca, _, err := generateCA()
	if err != nil {
		t.Fatalf("generateCA: %v", err)
	}
	return ca
}

func verifiedLeaf(t *testing.T, ca, leaf tls.Certificate, name string) *x509.Certificate {
	t.Helper()
	parsed, err := x509.ParseCertificate(leaf.Certificate[0])
	if err != nil {
		t.Fatalf("parse leaf: %v", err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca.Leaf)
	if _, err := parsed.Verify(x509.VerifyOptions{DNSName: name, Roots: roots}); err != nil {
		t.Fatalf("leaf does not verify for %q against the session CA: %v", name, err)
	}
	return parsed
}

func TestSignLeafIssuesADNSNameForAHostname(t *testing.T) {
	ca := testCA(t)
	leaf, err := signLeaf(ca, "api.anthropic.com")
	if err != nil {
		t.Fatalf("signLeaf: %v", err)
	}
	parsed := verifiedLeaf(t, ca, *leaf, "api.anthropic.com")
	if len(parsed.IPAddresses) != 0 {
		t.Errorf("IP SANs = %v, want none for a hostname", parsed.IPAddresses)
	}
	if !parsed.NotAfter.Equal(ca.Leaf.NotAfter) {
		t.Errorf("NotAfter = %v, want the CA's %v", parsed.NotAfter, ca.Leaf.NotAfter)
	}
}

func TestSignLeafIssuesAnIPAddressForAnIPLiteral(t *testing.T) {
	ca := testCA(t)
	leaf, err := signLeaf(ca, "160.79.104.10")
	if err != nil {
		t.Fatalf("signLeaf: %v", err)
	}
	parsed := verifiedLeaf(t, ca, *leaf, "160.79.104.10")
	if len(parsed.DNSNames) != 0 {
		t.Errorf("DNS SANs = %v, want none for an IP literal", parsed.DNSNames)
	}
	if len(parsed.IPAddresses) != 1 || !parsed.IPAddresses[0].Equal(net.ParseIP("160.79.104.10")) {
		t.Errorf("IP SANs = %v, want [160.79.104.10]", parsed.IPAddresses)
	}
}

func TestSignLeafPropagatesCryptoErrors(t *testing.T) {
	ca := testCA(t)
	withSwap(t, &generateECKey, failingECKey)
	if _, err := signLeaf(ca, "api.example"); err == nil {
		t.Error("signLeaf succeeded with a failing key generator, want the error")
	}
}

func TestSignLeafPropagatesSerialErrors(t *testing.T) {
	ca := testCA(t)
	withSwap(t, &randomSerial, func(io.Reader, *big.Int) (*big.Int, error) { return nil, errChannelSeam })
	if _, err := signLeaf(ca, "api.example"); err == nil {
		t.Error("signLeaf succeeded with a failing serial source, want the error")
	}
}

func TestSignLeafPropagatesCertificateErrors(t *testing.T) {
	ca := testCA(t)
	withSwap(t, &createCertificate, failingCreateCertificate)
	if _, err := signLeaf(ca, "api.example"); err == nil {
		t.Error("signLeaf succeeded with a failing certificate signer, want the error")
	}
}

func TestLeafIssuerReusesTheLeafPerName(t *testing.T) {
	issuer := newLeafIssuer(testCA(t))
	first, err := issuer.leafFor("api.example")
	if err != nil {
		t.Fatalf("leafFor: %v", err)
	}
	again, err := issuer.leafFor("api.example")
	if err != nil {
		t.Fatalf("leafFor: %v", err)
	}
	if first != again {
		t.Error("a second connection to the same name got a new leaf, want the cached one")
	}
	other, err := issuer.leafFor("other.example")
	if err != nil {
		t.Fatalf("leafFor: %v", err)
	}
	if other == first {
		t.Error("a different name reused another name's leaf")
	}
}

func TestLeafIssuerStopsCachingPastTheBound(t *testing.T) {
	issuer := newLeafIssuer(testCA(t))
	for index := range maxCachedLeaves {
		issuer.leaves["cached-"+strconv.Itoa(index)+".example"] = &tls.Certificate{}
	}
	leaf, err := issuer.leafFor("one-more.example")
	if err != nil {
		t.Fatalf("leafFor: %v", err)
	}
	if leaf == nil {
		t.Fatal("leafFor returned no leaf past the cache bound, want a freshly signed one")
	}
	if len(issuer.leaves) != maxCachedLeaves {
		t.Errorf("cached leaves = %d, want the bound %d", len(issuer.leaves), maxCachedLeaves)
	}
}

func TestLeafSerialsAreDistinct(t *testing.T) {
	ca := testCA(t)
	first, err := signLeaf(ca, "a.example")
	if err != nil {
		t.Fatalf("signLeaf: %v", err)
	}
	second, err := signLeaf(ca, "b.example")
	if err != nil {
		t.Fatalf("signLeaf: %v", err)
	}
	firstParsed, _ := x509.ParseCertificate(first.Certificate[0])
	secondParsed, _ := x509.ParseCertificate(second.Certificate[0])
	if firstParsed.SerialNumber.Cmp(secondParsed.SerialNumber) == 0 {
		t.Error("two leaves share a serial number")
	}
}

func TestLeafIssuerDoesNotCacheAFailedSignature(t *testing.T) {
	issuer := newLeafIssuer(testCA(t))
	withSwap(t, &generateECKey, failingECKey)
	if _, err := issuer.leafFor("api.example"); err == nil {
		t.Fatal("leafFor succeeded with a failing key generator")
	}
	if len(issuer.leaves) != 0 {
		t.Errorf("leaves = %v, want nothing cached after a failure", issuer.leaves)
	}
}

func TestMITMTLSConfigFallsBackToTheConnectTargetWithoutSNI(t *testing.T) {
	ca := testCA(t)
	var nameCalls []string
	config, err := mitmTLSConfig(ca, func(target, name string, sessionID uint16) {
		nameCalls = append(nameCalls, fmt.Sprintf("%s=%s#%d", target, name, sessionID))
	})("127.0.0.1:443", &goproxy.ProxyCtx{Session: 65537})
	if err != nil {
		t.Fatalf("mitmTLSConfig: %v", err)
	}
	leaf, err := config.GetCertificate(&tls.ClientHelloInfo{ServerName: ""})
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}
	verifiedLeaf(t, ca, *leaf, "127.0.0.1")

	named, err := config.GetCertificate(&tls.ClientHelloInfo{ServerName: "api.example"})
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}
	verifiedLeaf(t, ca, *named, "api.example")
	if len(nameCalls) != 1 || nameCalls[0] != "127.0.0.1:443=api.example#1" {
		t.Errorf("server names learned = %v, want the SNI and masked goproxy session", nameCalls)
	}
	if !config.SessionTicketsDisabled {
		t.Error("session tickets are on; a resumed handshake would skip GetCertificate and leave its tunnel unnamed")
	}
}

func failingECKey(_ elliptic.Curve, _ io.Reader) (*ecdsa.PrivateKey, error) {
	return nil, errChannelSeam
}

func failingCreateCertificate(_ io.Reader, _, _ *x509.Certificate, _, _ any) ([]byte, error) {
	return nil, errChannelSeam
}
