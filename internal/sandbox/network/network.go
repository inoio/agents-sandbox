package network

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
)

// Profile is a launcher network egress profile.
type Profile string

const (
	ProfilePublic  Profile = "public"
	ProfilePrivate Profile = "private"
	ProfileHost    Profile = "host"
	ProfileNone    Profile = "none"
	DefaultProfile Profile = ProfilePublic
)

func (p Profile) String() string { return string(p) }

// ParseProfile parses a profile string, erroring on unknown values.
func ParseProfile(s string) (Profile, error) {
	switch Profile(s) {
	case ProfilePublic, ProfilePrivate, ProfileHost, ProfileNone:
		return Profile(s), nil
	default:
		return "", fmt.Errorf("unknown network profile %q (want public, private, host, or none)", s)
	}
}

// ParseProfiles parses a list of profile names, erroring on an unknown value or
// when `none` is combined with another profile.
func ParseProfiles(in []string) ([]Profile, error) {
	profiles := make([]Profile, 0, len(in))
	for _, s := range in {
		p, err := ParseProfile(strings.TrimSpace(s))
		if err != nil {
			return nil, err
		}
		profiles = append(profiles, p)
	}
	// Dedupe before checking so repeating the same profile (e.g. `--network
	// none --network none`) is not mistaken for combining `none` with another.
	profiles = dedupeProfiles(profiles)
	if err := checkProfileCombination(profiles); err != nil {
		return nil, err
	}
	return profiles, nil
}

// checkProfileCombination rejects mixing the terminal `none` profile with any
// other, mirroring microsandbox's own treatment of none/all as whole-policy
// values rather than composable profiles.
func checkProfileCombination(profiles []Profile) error {
	if len(profiles) <= 1 {
		return nil
	}
	if slices.Contains(profiles, ProfileNone) {
		return errors.New(`network profile "none" cannot be combined with other profiles`)
	}
	return nil
}

// Policy is the resolved launcher network policy. The mapstructure tags let
// viper decode the nested `network:` YAML block (profile, egress-allow,
// egress-deny, dns-servers, intercept-tls, tls-bypass, disable-strict, proxy,
// observe) directly into this struct. The `profile` key accepts a single name, a
// comma-separated string, or a YAML list; viperconfig's string-to-slice decode
// hook normalizes the scalar forms into Profiles.
type Policy struct {
	Profiles []Profile `mapstructure:"profile"`
	// EgressAllow and EgressDeny have no env var and no CLI flag: they are set
	// in the config file only (see docs/configuration/networking.md). The
	// env:"-" tag keeps viperconfig's derived env list from binding them.
	EgressAllow []string `mapstructure:"egress-allow" env:"-"`
	EgressDeny  []string `mapstructure:"egress-deny"  env:"-"`
	DNSServers  []string `mapstructure:"dns-servers"`
	// InterceptTLS selects the sandbox's TLS interception mode; the empty value
	// means auto. TLSBypass lists hosts microsandbox must not intercept, and,
	// like the egress lists, is config-file-only. DisableStrict drops
	// microsandbox's strict-hostname enforcement, which otherwise denies a
	// request whose host the rule set does not match exactly.
	InterceptTLS  InterceptMode `mapstructure:"intercept-tls"`
	TLSBypass     []string      `mapstructure:"tls-bypass"     env:"-"`
	DisableStrict bool          `mapstructure:"disable-strict"`
	// Proxy is the outbound egress proxy. Value-typed so the derived config-key
	// registry recurses into the network.proxy.* keys; the zero value means no
	// proxy. It is creation-only, like the TLS-interception fields.
	Proxy Proxy `mapstructure:"proxy"`
	// Observe configures the learn-mode observation channel's terminating
	// proxy. It is launcher-side only: it never reaches the VM, the SDK spec, or
	// the fingerprint, so a change cannot recreate the VM.
	Observe ObserveConfig `mapstructure:"observe"`
	// upstreamCACerts holds paths to the CA bundles the runtime must trust
	// upstream. It is in-memory only: unexported and untagged, so the derived
	// config-key registry never sees it and no network.upstream-ca* key, env
	// var, or flag exists for it. It is set by WithUpstreamCACerts for the
	// learn-mode observation and copied by Spec.
	upstreamCACerts []string
}

// ObserveConfig configures the learn-mode observation channel's terminating
// proxy. Every field is launcher-side only: it shapes how the launcher runs its
// own proxy, never what the runtime projects into the VM.
type ObserveConfig struct {
	// SkipUpstreamVerify disables TLS certificate verification when the
	// observation channel's proxy dials the real upstream. The zero value
	// verifies, so a forwarded credential cannot be sent to an impostor; enable
	// it only when the host's egress is transparently intercepted with a private
	// CA the proxy's roots do not include.
	SkipUpstreamVerify bool `mapstructure:"skip-upstream-verify"`
}

// Empty reports whether the policy is unset (zero value).
func (p Policy) Empty() bool {
	return len(p.Profiles) == 0 && len(p.EgressAllow) == 0 && len(p.EgressDeny) == 0 &&
		len(p.DNSServers) == 0 && p.InterceptTLS == "" && len(p.TLSBypass) == 0 && !p.DisableStrict &&
		p.Proxy.Empty() && !p.Observe.SkipUpstreamVerify && len(p.upstreamCACerts) == 0
}

// Effective applies the default profile when no profile was selected.
func (p Policy) Effective() Policy {
	if len(p.Profiles) == 0 {
		p.Profiles = []Profile{DefaultProfile}
	}
	return p
}

// Fingerprint returns a stable SHA-256 hex digest of the policy, for detecting
// changes across runs. It hashes the profile and the sorted allow/deny/dns
// lists plus the TLS-interception settings and the outbound proxy, independent
// of the microsandbox SDK's canonical NetworkConfig shape.
//
// A deny-bearing policy that composes a profile also contributes a rule-order
// marker. The marker is not policy content: it records that the policy is
// emitted with egress-deny ahead of the profile allow rules. A VM created before
// that emission order holds the older digest in network_state.hash, so the
// marker makes the fingerprint differ and the VM is recreated once to pick up
// the corrected order; the marker reaches state after the recreate, so it fires
// only once. Policies whose emitted rules are identical either way (no deny
// entries, or the terminal `none` profile) get no marker and keep their digest
// stable.
//
// A `none` policy likewise contributes a marker for omitting the gateway-DNS
// rule, so a VM created with the rule is recreated once without it.
//
// The TLS-interception lines record the emitted TLS state, not the requested
// mode. Only `on` with no bypass emits a TLS config carrying an empty bypass,
// so it is the only case that contributes `intercept-tls=on`; a bypass host
// contributes a `tls-bypass=<host>` line whenever the mode is not `off` (under
// `off` the bypass is dropped and never reaches the VM, and `auto` with no
// bypass emits no TLS at all). `disable-strict=true` contributes when set. The
// result is that `auto`+bypass and `on`+bypass fingerprint alike — both emit
// `TLS{Bypass}` — and `off` and `auto` without bypass alike — both emit no TLS —
// so flipping between equivalent modes never forces a recreate.
//
// The proxy lines record the proxy as Spec emits it (protocol parsed, address
// trimmed), so a whitespace-padded configuration that reaches the VM like its
// canonical form fingerprints alike; an unset proxy contributes no lines.
func (p Policy) Fingerprint() string {
	p = p.Effective()
	var lines []string
	for _, prof := range dedupeProfiles(p.Profiles) {
		lines = append(lines, "profile="+prof.String())
	}
	for _, d := range p.DNSServers {
		lines = append(lines, "dns="+d)
	}
	for _, d := range p.EgressAllow {
		lines = append(lines, "allow="+d)
	}
	for _, d := range p.EgressDeny {
		lines = append(lines, "deny="+d)
	}
	mode := p.interceptMode()
	bypass := p.tlsBypass()
	switch {
	case mode == InterceptOn && len(bypass) == 0:
		// `on` with no bypass is the only case that emits a TLS config with an
		// empty bypass (the runtime intercepts, no host is skipped).
		lines = append(lines, "intercept-tls=on")
	case mode != InterceptOff:
		// `on` or `auto` with a bypass emits a TLS config carrying the bypass;
		// `auto` with none emits nothing.
		for _, host := range bypass {
			lines = append(lines, "tls-bypass="+host)
		}
	}
	if p.DisableStrict {
		lines = append(lines, "disable-strict=true")
	}
	if len(p.upstreamCACerts) > 0 {
		// The overlay's paths are not policy content read from disk: the line
		// hashes the joined path strings so changing the CA bundle path (or the
		// set of them) recreates the VM, without reading any file. Sort a clone
		// first, so the digest is order-independent like every other list field.
		sortedCerts := slices.Clone(p.upstreamCACerts)
		slices.Sort(sortedCerts)
		sum := sha256.Sum256([]byte(strings.Join(sortedCerts, "\n")))
		lines = append(lines, "upstream-ca="+hex.EncodeToString(sum[:]))
	}
	lines = append(lines, p.proxyLines("=")...)
	if p.denyPrecedesProfileAllow() {
		lines = append(lines, "rule-order=deny-before-profile")
	}
	if !p.allowsGatewayDNS() {
		lines = append(lines, "gateway-dns=omitted")
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

// Describe returns the effective policy as display lines named after the config
// keys: the deduplicated profiles, then the egress-allow, egress-deny, and DNS
// server entries, then the TLS-interception settings and the outbound proxy,
// each group sorted. Unlike Fingerprint it carries no emission markers, only
// what the user configured. An
// auto interception mode is the microsandbox default and unset means auto, so
// neither is displayed; only an explicit on or off is. Unlike Fingerprint, a
// bypass host is displayed even under `off`, since Describe reports the
// configured policy and does not model what reaches the VM. The proxy is
// normalized exactly as Spec emits it (protocol parsed, address trimmed), so a
// whitespace-padded configuration displays in its canonical form.
func (p Policy) Describe() []string {
	p = p.Effective()
	var lines []string
	lines = append(lines, prefixedSorted("profile: ", profileNames(dedupeProfiles(p.Profiles)))...)
	lines = append(lines, prefixedSorted("egress-allow: ", p.EgressAllow)...)
	lines = append(lines, prefixedSorted("egress-deny: ", p.EgressDeny)...)
	lines = append(lines, prefixedSorted("dns-server: ", p.DNSServers)...)
	if mode := p.interceptMode(); mode != InterceptAuto {
		lines = append(lines, "intercept-tls: "+string(mode))
	}
	lines = append(lines, prefixedSorted("tls-bypass: ", p.tlsBypass())...)
	if p.DisableStrict {
		lines = append(lines, "disable-strict: true")
	}
	lines = append(lines, p.proxyLines(": ")...)
	return lines
}

// proxyLines returns the proxy display lines, each key followed by separator:
// the normalized `protocol://address` and the optional username and
// password-env. An unset proxy yields no lines. The proxy is normalized
// exactly as Spec emits it (protocol parsed, address trimmed), so a
// whitespace-padded configuration displays in its canonical form.
func (p Policy) proxyLines(separator string) []string {
	if p.Proxy.Empty() {
		return nil
	}
	proxy := p.proxyEmission()
	lines := []string{"proxy" + separator + string(proxy.Protocol) + "://" + proxy.Address}
	if proxy.Username != "" {
		lines = append(lines, "proxy-username"+separator+proxy.Username)
	}
	if proxy.PasswordEnv != "" {
		lines = append(lines, "proxy-password-env"+separator+proxy.PasswordEnv)
	}
	return lines
}

// interceptMode returns the interception mode parsed exactly as Spec emits it,
// so a whitespace-padded value like "on " fingerprints and displays as "on"
// rather than as a distinct mode. An unknown value (rejected at config load and
// by Validate) is returned trimmed, so it still reads distinctly if it ever
// reaches the fingerprint.
func (p Policy) interceptMode() InterceptMode {
	mode, err := ParseInterceptMode(string(p.InterceptTLS))
	if err != nil {
		return InterceptMode(strings.TrimSpace(string(p.InterceptTLS)))
	}
	return mode
}

// tlsBypass returns the bypass hosts validated exactly as Spec emits them, so a
// whitespace-padded entry fingerprints and displays trimmed. ValidateTLSBypass
// only fails on an empty entry (rejected at config load and by Validate); on
// that error the hosts are returned as configured.
func (p Policy) tlsBypass() []string {
	hosts, err := ValidateTLSBypass(p.TLSBypass)
	if err != nil {
		return p.TLSBypass
	}
	return hosts
}

// proxyEmission returns the proxy exactly as Spec emits it, so a proxy that
// reaches the VM identically fingerprints identically. An invalid proxy (one
// Spec rejects) falls back to the trimmed configuration so it still
// fingerprints distinctly rather than reading as absent; an unset proxy yields
// the zero ProxySpec and contributes no lines.
func (p Policy) proxyEmission() msb.ProxySpec {
	spec, err := p.Proxy.spec()
	if err != nil {
		return msb.ProxySpec{
			Protocol:    msb.ProxyProtocol(strings.TrimSpace(string(p.Proxy.Protocol))),
			Address:     strings.TrimSpace(p.Proxy.Address),
			Username:    p.Proxy.Username,
			PasswordEnv: p.Proxy.PasswordEnv,
		}
	}
	return spec
}

func profileNames(profiles []Profile) []string {
	names := make([]string, 0, len(profiles))
	for _, profile := range profiles {
		names = append(names, profile.String())
	}
	return names
}

func prefixedSorted(prefix string, values []string) []string {
	lines := make([]string, 0, len(values))
	for _, value := range values {
		lines = append(lines, prefix+value)
	}
	sort.Strings(lines)
	return lines
}

// denyPrecedesProfileAllow reports whether the policy emits egress-deny rules
// ahead of the profile allow rules: it has at least one deny entry and at least
// one profile that contributes allow rules. When either is absent the emitted
// rules are identical either way, so the policy needs no rule-order marker.
func (p Policy) denyPrecedesProfileAllow() bool {
	if len(p.EgressDeny) == 0 {
		return false
	}
	for _, prof := range dedupeProfiles(p.Profiles) {
		if prof != ProfileNone {
			return true
		}
	}
	return false
}

// allowsGatewayDNS reports whether the policy emits the gateway-DNS rule. With
// that rule, microsandbox's DNS proxy forwards any name upstream, so a guest
// could exfiltrate data in query names without a single allow entry. The
// composable profiles allow broad egress anyway and keep it; `none` drops it,
// and the proxy then resolves only names an egress-allow rule matches.
func (p Policy) allowsGatewayDNS() bool {
	return !slices.Contains(p.Effective().Profiles, ProfileNone)
}

// NormalizeDNSServers validates and normalizes DNS upstream resolvers. Bare IPs
// (IPv4 or IPv6) get ":53" appended; "host:port" / "ip:port" entries are kept
// as-is. Empty entries, a host without a port, a non-numeric or out-of-range
// port, and garbage are rejected.
func NormalizeDNSServers(in []string) ([]string, error) {
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			return nil, errors.New("empty DNS server entry")
		}
		if net.ParseIP(s) != nil {
			out = append(out, net.JoinHostPort(s, "53"))
			continue
		}
		host, port, err := net.SplitHostPort(s)
		if err != nil {
			return nil, fmt.Errorf(
				"invalid DNS server %q: must be an IP (e.g. 1.1.1.1) or host:port (e.g. 1.1.1.1:53)",
				s,
			)
		}
		if host == "" {
			return nil, fmt.Errorf("invalid DNS server %q: missing host", s)
		}
		if port == "" {
			return nil, fmt.Errorf("invalid DNS server %q: missing port", s)
		}
		// strconv.Atoi returns 0 on a syntax error and MaxInt on an
		// out-of-range port (MinInt on negative overflow), so the range
		// check alone rejects every invalid value and the parse error is
		// safely ignored.
		portNumber, _ := strconv.Atoi(port)
		if portNumber < 1 || portNumber > 65535 {
			return nil, fmt.Errorf("invalid DNS server %q: invalid port %q", s, port)
		}
		out = append(out, net.JoinHostPort(host, port))
	}
	return out, nil
}

// Validate reports whether the policy is well-formed: known, combinable
// profiles, DNS servers that normalize, and a well-formed outbound proxy
// (known protocol, host:port address, no credentials a protocol cannot carry).
// It is the entry point the runoptions flag path (resolveNetwork) and
// vm.EnsureProjectVM run, so an invalid policy is rejected before any VM work.
// The env/config decode validates the same fields through viperconfig at load
// instead.
//
// Validate enforces only the configuration's own rules. It does not compose the
// proxy with the egress policy: the runtime dials the proxy host-side, so the
// dial bypasses the egress policy and needs no egress-allow entry for the proxy
// address (see the network-and-secrets interception-and-proxy spec).
func (p Policy) Validate() error {
	_, err := p.Spec()
	return err
}

// Spec converts the policy into a launcher network spec.
//
// `public`, `private`, and `host` compose: the launcher-owned adapter unions
// their allow rules (deduplicated, canonical order) and adds one narrow
// gateway-DNS rule. The `none` profile is launcher-only and selects no profile
// rules — deny-by-default egress, allow ingress — and no gateway-DNS rule, so
// only names matched by egress-allow resolve. It is an allowlist-only policy,
// not an airgap, and cannot be combined with another profile. A policy with no
// profile, including one with only DNSServers set, defaults to public.
//
// The egress lists are deduplicated, the DNS servers normalized, and the TLS
// interception mode, bypass hosts, and outbound proxy validated here, so the
// spec is the single validated input the adapter composes rules from.
func (p Policy) Spec() (msb.NetworkSpec, error) {
	profiles := dedupeProfiles(p.Effective().Profiles)
	if err := checkProfileCombination(profiles); err != nil {
		return msb.NetworkSpec{}, err
	}
	msbProfiles := make([]msb.NetworkProfile, 0, len(profiles))
	for _, prof := range profiles {
		switch prof {
		case ProfileNone:
			continue
		case ProfilePublic:
			msbProfiles = append(msbProfiles, msb.NetworkProfilePublic)
		case ProfilePrivate:
			msbProfiles = append(msbProfiles, msb.NetworkProfilePrivate)
		case ProfileHost:
			msbProfiles = append(msbProfiles, msb.NetworkProfileHost)
		default:
			return msb.NetworkSpec{}, fmt.Errorf("unknown network profile %q", prof)
		}
	}
	nameservers, err := NormalizeDNSServers(p.DNSServers)
	if err != nil {
		return msb.NetworkSpec{}, err
	}
	interceptMode, err := ParseInterceptMode(string(p.InterceptTLS))
	if err != nil {
		return msb.NetworkSpec{}, err
	}
	tlsBypass, err := ValidateTLSBypass(p.TLSBypass)
	if err != nil {
		return msb.NetworkSpec{}, err
	}
	proxy, err := p.Proxy.spec()
	if err != nil {
		return msb.NetworkSpec{}, err
	}
	return msb.NetworkSpec{
		Profiles:        msbProfiles,
		EgressAllow:     dedupe(p.EgressAllow),
		EgressDeny:      dedupe(p.EgressDeny),
		DNSServers:      nameservers,
		InterceptTLS:    msb.InterceptMode(interceptMode),
		TLSBypass:       tlsBypass,
		UpstreamCACerts: slices.Clone(p.upstreamCACerts),
		DisableStrict:   p.DisableStrict,
		Proxy:           proxy,
	}, nil
}

func dedupe(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// dedupeProfiles drops repeated profile names so the fingerprint and the SDK
// input ignore duplicates.
func dedupeProfiles(in []Profile) []Profile {
	seen := make(map[Profile]bool, len(in))
	out := make([]Profile, 0, len(in))
	for _, p := range in {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}
