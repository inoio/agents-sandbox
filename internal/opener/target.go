// Package opener lets processes inside the VM open links on the host. An
// in-VM xdg-open shim drops each request into a spool directory in the home
// volume; the launcher watches that directory through the volume's host path,
// checks the target against an allowlist compiled into the launcher, and hands
// it to the host's default handler.
package opener

import (
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// MaxTargetBytes bounds a single request so a runaway guest process cannot make
// the host read or spawn arbitrarily large arguments. Real auth URLs stay well
// below it.
const MaxTargetBytes = 8192

// allowedSchemes is the host-side allowlist. Every scheme activates a handler or
// opens a document and cannot run VM-supplied code. It is claude-code's own set
// plus mailto. file: is deliberately absent: on both macOS and Linux opening a
// file URL can launch an executable, which would be a guest-to-host code
// execution vector.
var allowedSchemes = map[string]bool{
	"http": true, "https": true, "mailto": true,
	"vscode": true, "vscode-insiders": true, "cursor": true, "windsurf": true, "zed": true,
	"jetbrains": true, "idea": true,
	"slack": true, "linear": true, "notion": true, "figma": true,
}

// redirectParameterNames are the query parameters an OAuth authorization URL
// uses to name its callback.
var redirectParameterNames = []string{"redirect_uri", "redirect_url", "redirectUri"}

// minimumRedirectPort excludes privileged ports: the launcher never binds them
// on the host on behalf of the VM.
const minimumRedirectPort = 1024

// Verdict is what the host does with a requested target.
type Verdict int

const (
	// VerdictRefuse means the target is not opened on the host.
	VerdictRefuse Verdict = iota
	// VerdictOpen means the target is opened with the host's default handler.
	VerdictOpen
	// VerdictLoopbackRedirect means the target is an OAuth authorization URL
	// whose callback points at a loopback listener inside the VM.
	VerdictLoopbackRedirect
)

// LoopbackRedirect is the in-VM callback named by an OAuth authorization URL.
type LoopbackRedirect struct {
	Port int
	Path string
}

// Classification is the host's decision about one target.
//
//exhaustruct:ignore
type Classification struct {
	Verdict  Verdict
	Reason   string
	Redirect LoopbackRedirect
}

func refuse(reason string) Classification {
	return Classification{Verdict: VerdictRefuse, Reason: reason}
}

// ClassifyTarget decides whether target may be opened on the host.
func ClassifyTarget(target string) Classification {
	if target == "" {
		return refuse("empty target")
	}
	if len(target) > MaxTargetBytes {
		return refuse("target too long")
	}
	if strings.ContainsFunc(target, isControlCharacter) {
		return refuse("target contains control characters")
	}
	parsed, err := url.Parse(target)
	if err != nil || !allowedSchemes[strings.ToLower(parsed.Scheme)] {
		return refuse("scheme not allowed (allowed: http, https, mailto and editor/app links)")
	}
	if !isWebScheme(parsed.Scheme) {
		return Classification{Verdict: VerdictOpen}
	}
	if parsed.Hostname() == "" {
		return refuse("URL has no host")
	}
	if isLoopbackHost(parsed.Hostname()) {
		return refuse("loopback URL refers to a server inside the VM, not on the host")
	}
	if isAmbiguousNumericHost(parsed.Hostname()) {
		return refuse("numeric host must be a plain IPv4 address")
	}
	return classifyRedirect(parsed.Query())
}

func classifyRedirect(query url.Values) Classification {
	for _, name := range redirectParameterNames {
		redirect, err := url.Parse(query.Get(name))
		if err != nil || !isWebScheme(redirect.Scheme) || !isLoopbackHost(redirect.Hostname()) {
			continue
		}
		// A non-numeric port makes Atoi return 0, and an out-of-range one the
		// extreme value; both fall outside the bounds below, so only the bounds
		// need checking.
		port, _ := strconv.Atoi(redirect.Port())
		if port < minimumRedirectPort || port > 65535 {
			return refuse("loopback redirect has no usable port")
		}
		path := redirect.Path
		if path == "" {
			path = "/"
		}
		return Classification{Verdict: VerdictLoopbackRedirect, Redirect: LoopbackRedirect{Port: port, Path: path}}
	}
	return Classification{Verdict: VerdictOpen}
}

func isWebScheme(scheme string) bool {
	scheme = strings.ToLower(scheme)
	return scheme == "http" || scheme == "https"
}

// isLoopbackHost reports whether a browser would connect to the host itself:
// localhost names, loopback addresses, and the unspecified address, which
// browsers on macOS and Linux route to loopback.
func isLoopbackHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	// A nil IP's methods all report false, so ParseIP's failure needs no guard.
	ip := net.ParseIP(host)
	return ip.IsLoopback() || ip.IsUnspecified()
}

// numericLabelPattern matches a host label that the WHATWG URL standard reads
// as a number (decimal, octal or 0x hex).
var numericLabelPattern = regexp.MustCompile(`^(0[xX][0-9a-fA-F]*|[0-9]+)$`)

// isAmbiguousNumericHost reports hosts that browsers parse as an IPv4 address
// although they are not a plain dotted quad, such as 127.1, 2130706433 or
// 0x7f.0.0.1. Browsers resolve them to an address — possibly loopback — that
// net.ParseIP does not recognize, so they are refused rather than normalized.
// Per WHATWG, a host is an IPv4 address when its last label is numeric.
func isAmbiguousNumericHost(host string) bool {
	host = strings.TrimSuffix(host, ".")
	if strings.Contains(host, ":") {
		return false
	}
	labels := strings.Split(host, ".")
	if !numericLabelPattern.MatchString(labels[len(labels)-1]) {
		return false
	}
	return !isPlainDottedQuad(labels)
}

func isPlainDottedQuad(labels []string) bool {
	if len(labels) != 4 {
		return false
	}
	for _, label := range labels {
		// Atoi's error value is 0, which Itoa renders as "0" != label, so the
		// round-trip check already rejects a non-numeric label.
		value, _ := strconv.Atoi(label)
		if value > 255 || strconv.Itoa(value) != label {
			return false
		}
	}
	return true
}

func isControlCharacter(character rune) bool {
	return character < 0x20 || character == 0x7f
}
