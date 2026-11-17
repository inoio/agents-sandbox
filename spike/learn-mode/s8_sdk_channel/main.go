// Command s8_sdk_channel is a throwaway, host-only probe for the four channel
// questions S1 must settle
// (plans/2026-11-17-learn-mode-sdk-semantics.md), which are exactly the four
// open items the specs pin on it:
//
//   - q1 topology: does the runtime re-originate an intercepted, secret-
//     substituted request to a host endpoint we own (T2), or must the launcher
//     intercept the guest itself (T1)?
//   - q2 off: with a secret configured, does NetworkConfig.TLS == nil actually
//     disable interception, or does the runtime re-enable it?
//   - q3 / q3s composition: is a proxy we configure reachable under bare `none`
//     without an egress-allow rule for its address? (HTTP CONNECT and SOCKS5.)
//   - q4 bypass: does a host that is both tls-bypassed and egress-allowlisted
//     still pass under strict hostname mode?
//
// It drives the pinned microsandbox SDK v0.7.15 directly and prints, per case, a
// verdict line plus a diagnostic block: the guest's own output, the runtime's
// system log tail, and the observer's connection log. It needs a KVM host with
// the microsandbox runtime; run it through run.sh. It prints header names and
// booleans, never a header value.
package main

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"flag"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

// The placeholder is explicit (not SDK-generated) so the observer can tell the
// placeholder apart from the real value at the proxy. The real value is a
// throwaway token; nothing here is a real credential.
const (
	secretEnv   = "SPIKE_TOKEN"
	realToken   = "spike-real-token-0123456789"
	placeholder = "spike-placeholder-0123456789"
)

var (
	flagCases   = flag.String("case", "all", "comma-separated cases: q0,q0o,q1,q2,q3,q3o,q3s,q3so,q4,q4o or all")
	flagTimeout = flag.Duration("timeout", 45*time.Second, "per-sandbox command timeout")
)

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "  obs: "+format+"\n", args...)
}

func main() {
	flag.Parse()
	image := envOr("SPIKE_IMAGE", "alpine:3.20")
	allowedHost := envOr("SPIKE_ALLOWED_HOST", "example.com")
	bypassHost := envOr("SPIKE_BYPASS_HOST", "www.iana.org")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	home, err := prepareRuntime(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "runtime setup: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "runtime home: %s\n", home)

	for _, name := range strings.Split(*flagCases, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if name == "all" {
			for _, ordered := range []string{"q0", "q0o", "q1", "q2", "q3", "q3o", "q3s", "q3so", "q4", "q4o"} {
				fmt.Println(runCase(ctx, ordered, image, allowedHost, bypassHost))
			}
			continue
		}
		fmt.Println(runCase(ctx, name, image, allowedHost, bypassHost))
	}
}

// prepareRuntime gives the probe its own microsandbox home and installs the
// SDK-pinned runtime into it, mirroring what the launcher does before it
// creates a sandbox. Without this, CreateSandbox resolves a runtime from the
// default ~/.microsandbox — where a manual `msb` install leaves a runtime the
// SDK's FFI refuses ("does not advertise protected sandbox state"). MSB_HOME
// must be set: the SDK resolves all non-setup operations through it, while
// RuntimeConfig{Home} alone only governs runtime setup.
func prepareRuntime(ctx context.Context) (string, error) {
	home := os.Getenv("SPIKE_MSB_HOME")
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		home = filepath.Join(userHome, ".microsandbox-spike")
	}
	if err := os.MkdirAll(home, 0o750); err != nil {
		return "", err
	}
	if err := os.Setenv("MSB_HOME", home); err != nil {
		return "", err
	}
	if _, err := msb.EnsureRuntime(ctx, msb.RuntimeConfig{Home: home}, msb.InstallOptions{}); err != nil {
		return home, err
	}
	return home, nil
}

func runCase(ctx context.Context, name, image, allowedHost, bypassHost string) verdict {
	switch name {
	case "q0":
		return caseAllow(ctx, image, allowedHost, true)
	case "q0o":
		return caseAllow(ctx, image, allowedHost, false)
	case "q2":
		return caseOff(ctx, image, allowedHost) // no observer: proxy-free by design
	case "q4":
		return caseBypass(ctx, image, bypassHost, true)
	case "q4o":
		return caseBypass(ctx, image, bypassHost, false)
	}
	obs, err := newObserver(observerMode(name))
	if err != nil {
		return verdict{Case: name, Detail: "observer: " + err.Error()}
	}
	switch name {
	case "q1":
		return caseTopology(ctx, obs, image, allowedHost)
	case "q3":
		return caseComposition(ctx, obs, image, allowedHost, "http", true)
	case "q3o":
		return caseComposition(ctx, obs, image, allowedHost, "http", false)
	case "q3s":
		return caseComposition(ctx, obs, image, allowedHost, "socks5", true)
	case "q3so":
		return caseComposition(ctx, obs, image, allowedHost, "socks5", false)
	default:
		return verdict{Case: name, Detail: "unknown case"}
	}
}

func observerMode(caseID string) string {
	if strings.HasPrefix(caseID, "q3") {
		return "blind"
	}
	return "terminate"
}

type verdict struct {
	Case      string
	Connected bool
	ExitCode  int
	Endpoint  string // real | placeholder | tls-failed | connect-only | blocked | leaked | direct | none
	Header    string // credential-bearing header names the endpoint saw
	Detail    string // newline-joined diagnostics
}

func (v verdict) String() string {
	head := fmt.Sprintf(
		"verdict case=%s connected=%s exit=%d endpoint=%s header=%s",
		v.Case, yesNo(v.Connected), v.ExitCode, v.Endpoint, orNone(v.Header),
	)
	if strings.TrimSpace(v.Detail) == "" {
		return head
	}
	var builder strings.Builder
	builder.WriteString(head)
	builder.WriteString("\n")
	for _, line := range strings.Split(strings.TrimRight(v.Detail, "\n"), "\n") {
		builder.WriteString("    | ")
		builder.WriteString(line)
		builder.WriteString("\n")
	}
	return strings.TrimRight(builder.String(), "\n")
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func orNone(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func combine(out guestResult, sb *msb.Sandbox, ctx context.Context) string {
	var builder strings.Builder
	builder.WriteString("guest stdout+stderr:\n")
	builder.WriteString(strings.TrimRight(out.stdout, "\n"))
	builder.WriteString("\n")
	if strings.TrimSpace(out.stderr) != "" {
		builder.WriteString(strings.TrimRight(out.stderr, "\n"))
		builder.WriteString("\n")
	}
	builder.WriteString("runtime system log (tail):\n")
	builder.WriteString(systemLogTail(ctx, sb))
	return builder.String()
}

// caseTopology answers Q1: the runtime intercepts (TLS set, trusting our proxy
// as an upstream CA), substitutes the placeholder, and re-originates to our
// proxy. If our endpoint sees the real value, the chain is T2.
func caseTopology(ctx context.Context, obs *observer, image, allowedHost string) verdict {
	const name = "q1-topology"
	network := noneAllow(true, allowedHost)
	network.TLS = &msb.TLSConfig{UpstreamCACerts: []string{obs.caFile}}
	sb, err := createSandbox(ctx, "spike-s8-q1", image,
		msb.WithNetwork(network),
		msb.WithProxy(msb.HTTPConnectProxy(obs.addr)),
		msb.WithSecrets(spikeSecret(allowedHost)),
	)
	if err != nil {
		return verdict{Case: name, Detail: "create: " + err.Error()}
	}
	defer destroy(sb)
	before := obs.mark()
	out := guestWget(ctx, sb, allowedHost)
	return interceptedVerdict(name, obs, before, out, combine(out, sb, ctx))
}

// caseOff answers Q2 without a proxy, so the question is not tangled with our
// own CA trust. TLS is left nil; the secret's Allow excludes the requested
// host, so a non-allowed placeholder is a policy violation. If the runtime
// re-enables interception despite TLS == nil, it sees the placeholder and
// blocks it; if `off` is honoured, nothing intercepts and the placeholder
// reaches the host unsubstituted.
func caseOff(ctx context.Context, image, allowedHost string) verdict {
	const name = "q2-off"
	sb, err := createSandbox(ctx, "spike-s8-q2", image,
		msb.WithNetwork(noneAllow(true, allowedHost)),
		msb.WithSecrets(blockingSecret("api."+allowedHost)),
	)
	if err != nil {
		return verdict{Case: name, Detail: "create: " + err.Error()}
	}
	defer destroy(sb)
	out := guestWget(ctx, sb, allowedHost)
	endpoint := "leaked"
	if !out.success || strings.Contains(out.stdout, "denied") || strings.Contains(out.stdout, "403") {
		endpoint = "blocked"
	}
	return verdict{
		Case:      name,
		Connected: out.success,
		ExitCode:  out.exit,
		Endpoint:  endpoint,
		Detail:    combine(out, sb, ctx),
	}
}

// caseAllow answers the strict-mode control for q3/q4: with no proxy and no
// secret, does a plain-domain egress-allow rule authorize a non-intercepted
// connection? strict on is the runtime default; off is the old spike's
// --net-strict=false. The config mirrors the launcher's `none` exactly (no
// gateway-DNS rule).
func caseAllow(ctx context.Context, image, allowedHost string, strict bool) verdict {
	name := "q0-allow"
	if !strict {
		name += "-strict-off"
	}
	network := noneAllow(strict, allowedHost)
	network.Rules = network.Rules[1:] // drop the gateway-DNS rule the launcher omits
	sb, err := createSandbox(ctx, "spike-s8-q0", image, msb.WithNetwork(network))
	if err != nil {
		return verdict{Case: name, Detail: "create: " + err.Error()}
	}
	defer destroy(sb)
	out := guestWget(ctx, sb, allowedHost)
	endpoint := "denied"
	if out.success {
		endpoint = "allowed"
	}
	return verdict{
		Case:      name,
		Connected: out.success,
		ExitCode:  out.exit,
		Endpoint:  endpoint,
		Detail:    combine(out, sb, ctx),
	}
}

// caseComposition answers Q3: with a proxy configured and bare `none`, is the
// proxy reachable? The observer is blind, so the CONNECT (HTTP) or the SOCKS5
// handshake arriving at it is the reachability evidence. strict mirrors the
// runtime's default strict-hostname mode; the `o` variants turn it off, which
// the old spike's `none` cases did with --net-strict=false.
func caseComposition(ctx context.Context, obs *observer, image, allowedHost, protocol string, strict bool) verdict {
	name := "q3-composition"
	if !strict {
		name += "-strict-off"
	}
	proxy := msb.HTTPConnectProxy(obs.addr)
	if protocol == "socks5" {
		name = "q3s-composition-socks5"
		if !strict {
			name += "-strict-off"
		}
		proxy = msb.SOCKS5Proxy(obs.socksAddr)
	}
	sb, err := createSandbox(
		ctx,
		"spike-s8-q3",
		image,
		msb.WithNetwork(noneAllow(strict, allowedHost)),
		msb.WithProxy(proxy),
	)
	if err != nil {
		return verdict{Case: name, Detail: "create: " + err.Error()}
	}
	defer destroy(sb)
	before := obs.mark()
	out := guestWget(ctx, sb, allowedHost)
	conns := obs.since(before)
	endpoint := "none"
	if len(conns) > 0 {
		endpoint = "connect-only"
	}
	return verdict{
		Case:      name,
		Connected: len(conns) > 0,
		ExitCode:  out.exit,
		Endpoint:  endpoint,
		Header:    observedHosts(conns),
		Detail:    combine(out, sb, ctx),
	}
}

// caseBypass answers Q4: a host that is both tls-bypassed and egress-allowlisted
// is requested, with strict hostname mode on (the runtime default) or off.
func caseBypass(ctx context.Context, image, bypassHost string, strict bool) verdict {
	name := "q4-bypass"
	if !strict {
		name += "-strict-off"
	}
	network := noneAllow(strict, bypassHost)
	network.TLS = &msb.TLSConfig{Bypass: []string{bypassHost}}
	sb, err := createSandbox(ctx, "spike-s8-q4", image,
		msb.WithNetwork(network),
		// The secret allows a different host, so the bypassed host is not
		// substituted and the placeholder travels as-is.
		msb.WithSecrets(spikeSecret("api."+bypassHost)),
	)
	if err != nil {
		return verdict{Case: name, Detail: "create: " + err.Error()}
	}
	defer destroy(sb)
	out := guestWget(ctx, sb, bypassHost)
	endpoint := "direct-blocked"
	if out.success {
		endpoint = "direct"
	}
	return verdict{
		Case:      name,
		Connected: out.success,
		ExitCode:  out.exit,
		Endpoint:  endpoint,
		Detail:    combine(out, sb, ctx),
	}
}

func interceptedVerdict(name string, obs *observer, before int, out guestResult, detail string) verdict {
	conns := obs.since(before)
	endpoint, header := "none", ""
	sawTLSFailure := false
	for _, c := range conns {
		switch {
		case c.SawReal:
			endpoint = "real"
		case c.SawPlaceholder && endpoint != "real":
			endpoint = "placeholder"
		}
		if !c.TLSCompleted {
			sawTLSFailure = true
		}
		if c.HeaderNames != "" {
			header = c.HeaderNames
		}
	}
	if endpoint == "none" && sawTLSFailure {
		endpoint = "tls-failed"
	}
	return verdict{
		Case:      name,
		Connected: out.success,
		ExitCode:  out.exit,
		Endpoint:  endpoint,
		Header:    header,
		Detail:    detail,
	}
}

func observedHosts(conns []observedConn) string {
	hosts := make([]string, 0, len(conns))
	for _, c := range conns {
		hosts = append(hosts, c.Host)
	}
	return strings.Join(hosts, ",")
}

func systemLogTail(ctx context.Context, sb *msb.Sandbox) string {
	entries, err := sb.Logs(ctx, msb.LogOptions{Sources: []msb.LogSource{msb.LogSourceSystem}, Tail: 80})
	if err != nil {
		return "(system log unavailable: " + err.Error() + ")"
	}
	if len(entries) == 0 {
		return "(empty)"
	}
	var builder strings.Builder
	for _, entry := range entries {
		builder.WriteString(strings.TrimRight(entry.Text(), "\n"))
		builder.WriteString("\n")
	}
	return builder.String()
}

type guestResult struct {
	exit    int
	success bool
	stdout  string
	stderr  string
}

func guestWget(ctx context.Context, sb *msb.Sandbox, host string) guestResult {
	command := fmt.Sprintf(`echo "== env (ssl/cert/ca) =="; env | grep -iE 'ssl|cert|ca_' || true
echo "== /.msb/tls =="; ls -la /.msb/tls/ 2>&1 || true
echo "== request =="
wget -q -S -O /dev/null -T 20 --header "Authorization: $%s" %s 2>&1
echo "__WGET_EXIT=$?"`, secretEnv, "https://"+host+"/")
	out, err := sb.Shell(ctx, command,
		msb.WithExecTimeout(*flagTimeout),
		msb.WithExecEnv(map[string]string{secretEnv: placeholder}),
	)
	if err != nil {
		return guestResult{stderr: "exec: " + err.Error()}
	}
	stdout := out.Stdout()
	exit := parseExit(stdout)
	stderr := strings.TrimSpace(out.Stderr())
	if len(stderr) > 400 {
		stderr = stderr[:400]
	}
	return guestResult{exit: exit, success: exit == 0, stdout: stdout, stderr: stderr}
}

// parseExit reads the "__WGET_EXIT=<n>" line the guest script prints, so the
// verdict reflects wget's own status rather than the wrapper's echo.
func parseExit(stdout string) int {
	for _, line := range strings.Split(stdout, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "__WGET_EXIT="); ok {
			var code int
			if _, err := fmt.Sscanf(rest, "%d", &code); err == nil {
				return code
			}
		}
	}
	return -1
}

func spikeSecret(allowedHost string) msb.SecretEntry {
	headers := true
	return msb.Secret.Env(secretEnv, realToken, msb.SecretEnvOptions{
		Allow:       []string{allowedHost},
		Placeholder: placeholder,
		Substitution: msb.SecretSubstitution{
			Headers:      &headers,
			HeaderFields: []string{"authorization"},
		},
	})
}

func blockingSecret(allowedHost string) msb.SecretEntry {
	headers := true
	return msb.Secret.Env(secretEnv, realToken, msb.SecretEnvOptions{
		Allow:           []string{allowedHost},
		Placeholder:     placeholder,
		ViolationAction: msb.ViolationActionBlockAndLog,
		Substitution: msb.SecretSubstitution{
			Headers:      &headers,
			HeaderFields: []string{"authorization"},
		},
	})
}

func noneAllow(strict bool, hosts ...string) *msb.NetworkConfig {
	rules := []msb.PolicyRule{msb.Rule.AllowDNS()}
	for _, host := range hosts {
		rules = append(rules, msb.PolicyRule{
			Action:      msb.PolicyActionAllow,
			Direction:   msb.PolicyDirectionEgress,
			Destination: host,
		})
	}
	return &msb.NetworkConfig{
		Rules:          rules,
		DefaultEgress:  msb.PolicyActionDeny,
		DefaultIngress: msb.PolicyActionAllow,
		DisableStrict:  !strict,
	}
}

func createSandbox(ctx context.Context, name, image string, opts ...msb.SandboxOption) (*msb.Sandbox, error) {
	base := []msb.SandboxOption{msb.WithImage(image), msb.WithReplace(), msb.WithLogLevel(msb.LogLevelDebug)}
	return msb.CreateSandbox(ctx, name, append(base, opts...)...)
}

func destroy(sb *msb.Sandbox) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	_ = sb.Destroy(ctx)
}

// --- the observer -----------------------------------------------------------

// observedConn is one CONNECT the observer accepted, plus, when it terminated
// TLS, the credential-bearing header names of the single inner request and
// whether the real value or the placeholder crossed it.
type observedConn struct {
	Host           string
	TLSCompleted   bool
	HeaderNames    string
	SawReal        bool
	SawPlaceholder bool
}

type observer struct {
	mode      string // "terminate" or "blind"
	addr      string
	socksAddr string
	caFile    string
	ca        *x509.Certificate
	caKey     *ecdsa.PrivateKey

	mu    sync.Mutex
	conns []observedConn
}

func newObserver(mode string) (*observer, error) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "spike-s8-ca"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, err
	}
	ca, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})

	caFile, err := os.CreateTemp("", "spike-s8-ca-*.pem")
	if err != nil {
		return nil, err
	}
	if _, err := caFile.Write(pemBytes); err != nil {
		return nil, err
	}
	if err := caFile.Close(); err != nil {
		return nil, err
	}

	bindHost := envOr("SPIKE_PROXY_HOST", "127.0.0.1")
	listener, err := net.Listen("tcp", bindHost+":0")
	if err != nil {
		return nil, err
	}
	socksListener, err := net.Listen("tcp", bindHost+":0")
	if err != nil {
		return nil, err
	}
	obs := &observer{
		mode:      mode,
		addr:      listener.Addr().String(),
		socksAddr: socksListener.Addr().String(),
		caFile:    caFile.Name(),
		ca:        ca,
		caKey:     caKey,
	}
	logf("listening %s (http %s, socks %s)", mode, obs.addr, obs.socksAddr)
	go obs.serve(listener, obs.handle)
	go obs.serve(socksListener, obs.handleSocks)
	return obs, nil
}

func (o *observer) mark() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.conns)
}

func (o *observer) since(before int) []observedConn {
	o.mu.Lock()
	defer o.mu.Unlock()
	if before > len(o.conns) {
		return nil
	}
	out := make([]observedConn, len(o.conns)-before)
	copy(out, o.conns[before:])
	return out
}

func (o *observer) record(c observedConn) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.conns = append(o.conns, c)
}

func (o *observer) serve(listener net.Listener, handle func(net.Conn)) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		go handle(conn)
	}
}

func (o *observer) handle(conn net.Conn) {
	defer conn.Close()
	logf("accepted %s", conn.RemoteAddr())
	_ = conn.SetDeadline(time.Now().Add(2 * time.Minute))

	reader := bufio.NewReader(conn)
	request, err := http.ReadRequest(reader)
	if err != nil {
		logf("http parse error: %v", err)
		return
	}
	host := stripPort(request.Host)
	// Record before responding: the CONNECT arriving is the reachability fact.
	if o.mode == "blind" {
		o.record(observedConn{Host: host})
		logf("CONNECT %s (blind)", host)
	}
	if _, err := conn.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n")); err != nil {
		logf("200 write failed: %v", err)
		return
	}
	if o.mode == "blind" {
		return
	}
	o.terminate(bufferedConn{Conn: conn, reader: reader}, host)
}

// handleSocks accepts a SOCKS5 CONNECT (no-auth) and hands the tunnelled bytes
// to the same observer as the HTTP path. A minimal implementation: the probe
// only needs the destination and, in terminate mode, the inner TLS request.
func (o *observer) handleSocks(conn net.Conn) {
	defer conn.Close()
	logf("accepted socks from %s", conn.RemoteAddr())
	_ = conn.SetDeadline(time.Now().Add(2 * time.Minute))

	reader := bufio.NewReader(conn)
	version, err := reader.ReadByte()
	if err != nil || version != 5 {
		logf("socks: bad version byte (%v, %d)", err, version)
		return
	}
	methodCount, err := reader.ReadByte()
	if err != nil {
		return
	}
	if _, err := io.CopyN(io.Discard, reader, int64(methodCount)); err != nil {
		return
	}
	if _, err := conn.Write([]byte{5, 0}); err != nil { // no authentication
		return
	}
	header := make([]byte, 4)
	if _, err := io.ReadFull(reader, header); err != nil || header[0] != 5 || header[1] != 1 {
		logf("socks: bad request header (%v)", err)
		return
	}
	host, err := readSocksAddress(reader, header[3])
	if err != nil {
		logf("socks: address: %v", err)
		return
	}
	if _, err := io.CopyN(io.Discard, reader, 2); err != nil { // port
		return
	}
	if o.mode == "blind" {
		o.record(observedConn{Host: host})
		logf("SOCKS5 CONNECT %s (blind)", host)
	}
	if _, err := conn.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}
	if o.mode == "blind" {
		return
	}
	o.terminate(bufferedConn{Conn: conn, reader: reader}, host)
}

func readSocksAddress(reader *bufio.Reader, addressType byte) (string, error) {
	switch addressType {
	case 1: // IPv4
		buf := make([]byte, 4)
		if _, err := io.ReadFull(reader, buf); err != nil {
			return "", err
		}
		return net.IP(buf).String(), nil
	case 3: // domain name
		length, err := reader.ReadByte()
		if err != nil {
			return "", err
		}
		buf := make([]byte, int(length))
		if _, err := io.ReadFull(reader, buf); err != nil {
			return "", err
		}
		return string(buf), nil
	case 4: // IPv6
		buf := make([]byte, 16)
		if _, err := io.ReadFull(reader, buf); err != nil {
			return "", err
		}
		return net.IP(buf).String(), nil
	default:
		return "", fmt.Errorf("unsupported SOCKS5 address type %d", addressType)
	}
}

// bufferedConn keeps any bytes a bufio.Reader read past the handshake so the
// TLS server sees the client's ClientHello.
type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c bufferedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }

func (o *observer) terminate(conn net.Conn, host string) {
	// The runtime may CONNECT to the resolved IP while validating the upstream
	// certificate against the original SNI, so the leaf must follow the SNI
	// (falling back to the CONNECT host when the client sends none).
	config := &tls.Config{
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			name := hello.ServerName
			if name == "" {
				name = host
			}
			logf("SNI %q (connect host %s)", hello.ServerName, host)
			return o.certFor(name)
		},
	}
	tlsConn := tls.Server(conn, config)
	if err := tlsConn.Handshake(); err != nil {
		logf("TLS handshake with %s failed: %v", host, err)
		o.record(observedConn{Host: host})
		return
	}
	inner, err := http.ReadRequest(bufio.NewReader(tlsConn))
	if err != nil {
		logf("TLS ok for %s, inner parse failed: %v", host, err)
		o.record(observedConn{Host: host, TLSCompleted: true})
		return
	}
	observed := observedConn{Host: host, TLSCompleted: true}
	names := make([]string, 0, len(inner.Header))
	for name, values := range inner.Header {
		lower := strings.ToLower(name)
		if isCredentialHeader(lower) {
			names = append(names, lower)
		}
		for _, value := range values {
			if strings.Contains(value, realToken) {
				observed.SawReal = true
			}
			if strings.Contains(value, placeholder) {
				observed.SawPlaceholder = true
			}
		}
	}
	observed.HeaderNames = strings.Join(names, ",")
	logf(
		"TLS ok for %s: headers=%q real=%v placeholder=%v",
		host,
		observed.HeaderNames,
		observed.SawReal,
		observed.SawPlaceholder,
	)
	o.record(observed)

	_, _ = tlsConn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nok"))
}

func (o *observer) certFor(host string) (*tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(now.UnixNano()),
		Subject:      pkix.Name{CommonName: host},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if ip := net.ParseIP(host); ip != nil {
		template.IPAddresses = []net.IP{ip}
	} else {
		template.DNSNames = []string{host}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, o.ca, &key.PublicKey, o.caKey)
	if err != nil {
		return nil, err
	}
	return &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, nil
}

func isCredentialHeader(name string) bool {
	switch name {
	case "authorization", "x-api-key", "api-key", "proxy-authorization", "cookie":
		return true
	default:
		return false
	}
}

func stripPort(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
}
