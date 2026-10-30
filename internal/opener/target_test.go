package opener

import (
	"strconv"
	"strings"
	"testing"
)

func TestClassifyTargetAllowsListedSchemes(t *testing.T) {
	for _, target := range []string{
		"https://example.com/path?q=1",
		"http://example.com",
		"HTTPS://EXAMPLE.COM",
		"mailto:someone@example.com",
		"vscode://file/workspace/main.go",
		"vscode-insiders://file/x",
		"cursor://file/x",
		"windsurf://file/x",
		"zed://file/x",
		"jetbrains://idea/navigate",
		"idea://open?file=x",
		"slack://open",
		"linear://issue/1",
		"notion://www.notion.so/x",
		"figma://file/x",
	} {
		t.Run(target, func(t *testing.T) {
			classification := ClassifyTarget(target)
			if classification.Verdict != VerdictOpen {
				t.Fatalf("ClassifyTarget(%q) = %+v, want open", target, classification)
			}
		})
	}
}

func TestClassifyTargetRefusesUnsafeOrUnknownTargets(t *testing.T) {
	for _, testCase := range []struct {
		target string
		reason string
	}{
		{"file:///etc/passwd", "scheme"},
		{"/home/dev/notes.txt", "scheme"},
		{"-a Calculator", "scheme"},
		{"javascript:alert(1)", "scheme"},
		{"smb://host/share", "scheme"},
		{"", "empty"},
		{"https://", "host"},
		{"https://example.com/\x00", "control"},
		{"https://example.com/\nnext", "control"},
		{strings.Repeat("a", MaxTargetBytes+1), "too long"},
	} {
		t.Run(testCase.target, func(t *testing.T) {
			classification := ClassifyTarget(testCase.target)
			if classification.Verdict != VerdictRefuse {
				t.Fatalf("ClassifyTarget(%q) = %+v, want refuse", testCase.target, classification)
			}
			if !strings.Contains(classification.Reason, testCase.reason) {
				t.Fatalf("reason %q does not mention %q", classification.Reason, testCase.reason)
			}
		})
	}
}

func TestClassifyTargetRefusesLoopbackURLs(t *testing.T) {
	for _, target := range []string{
		"http://localhost:3000/",
		"http://127.0.0.1:8080/app",
		"http://127.0.0.2/",
		"http://[::1]:5173/",
		"https://LOCALHOST/",
		"http://app.localhost:3000/",
		"http://localhost.:3000/",
		"http://127.0.0.1.:3000/",
		"http://0.0.0.0:8080/",
		"http://[::]:8080/",
		"http://[::ffff:127.0.0.1]:8080/",
	} {
		t.Run(target, func(t *testing.T) {
			classification := ClassifyTarget(target)
			if classification.Verdict != VerdictRefuse || !strings.Contains(classification.Reason, "VM") {
				t.Fatalf("ClassifyTarget(%q) = %+v, want refuse mentioning the VM", target, classification)
			}
		})
	}
}

// TestClassifyTargetRefusesAmbiguousNumericHosts covers IPv4 spellings that
// browsers accept but net.ParseIP does not, which could otherwise smuggle a
// loopback address past the check.
func TestClassifyTargetRefusesAmbiguousNumericHosts(t *testing.T) {
	for _, target := range []string{
		"http://127.1:8080/",
		"http://2130706433:8080/",
		"http://0x7f.0.0.1/",
		"http://0177.0.0.1/",
		"http://127.0.0.01/",
		"http://example.123/",
		"http://127.1.:8080/",
		"http://1.2.3.256/",
		"http://1.2.3.01/",
	} {
		t.Run(target, func(t *testing.T) {
			if classification := ClassifyTarget(target); classification.Verdict != VerdictRefuse {
				t.Fatalf("ClassifyTarget(%q) = %+v, want refuse", target, classification)
			}
		})
	}
}

func TestClassifyTargetAllowsPlainHosts(t *testing.T) {
	for _, target := range []string{
		"https://93.184.215.14/",
		"https://1.2.3.255/",
		"https://example.com./",
		"https://face.be/",
		"https://cafe.de/",
		"https://a.b0x1/",
		"https://[2606:4700::1111]/",
	} {
		t.Run(target, func(t *testing.T) {
			if classification := ClassifyTarget(target); classification.Verdict != VerdictOpen {
				t.Fatalf("ClassifyTarget(%q) = %+v, want open", target, classification)
			}
		})
	}
}

func TestClassifyTargetDetectsLoopbackRedirect(t *testing.T) {
	for _, testCase := range []struct {
		target string
		port   int
		path   string
	}{
		{"https://claude.ai/oauth/authorize?client_id=x&redirect_uri=http%3A%2F%2Flocalhost%3A43117%2Fcallback&state=s", 43117, "/callback"},
		{"https://auth.openai.com/oauth/authorize?redirect_uri=http://localhost:1455/auth/callback", 1455, "/auth/callback"},
		{"https://gitlab.com/oauth/authorize?redirect_uri=http%3A%2F%2F127.0.0.1%3A8080%2Fcallback", 8080, "/callback"},
		{"https://x.example/authorize?redirect_uri=http%3A%2F%2Flocalhost%3A43117", 43117, "/"},
		{"https://mcp.example.com/authorize?redirect_uri=http%3A%2F%2F%5B%3A%3A1%5D%3A19876%2Fmcp%2Foauth%2Fcallback", 19876, "/mcp/oauth/callback"},
	} {
		t.Run(testCase.target, func(t *testing.T) {
			classification := ClassifyTarget(testCase.target)
			if classification.Verdict != VerdictLoopbackRedirect {
				t.Fatalf("ClassifyTarget = %+v, want loopback redirect", classification)
			}
			if classification.Redirect.Port != testCase.port || classification.Redirect.Path != testCase.path {
				t.Fatalf("redirect = %+v, want port %d path %s", classification.Redirect, testCase.port, testCase.path)
			}
		})
	}
}

func TestClassifyTargetIgnoresNonLoopbackRedirect(t *testing.T) {
	target := "https://claude.ai/oauth/authorize?redirect_uri=https%3A%2F%2Fplatform.claude.com%2Foauth%2Fcode%2Fcallback"
	if classification := ClassifyTarget(target); classification.Verdict != VerdictOpen {
		t.Fatalf("ClassifyTarget = %+v, want open", classification)
	}
}

func TestClassifyTargetRefusesUnusableLoopbackRedirectPorts(t *testing.T) {
	for _, target := range []string{
		"https://x.example/a?redirect_uri=http%3A%2F%2Flocalhost%2Fcallback",
		"https://x.example/a?redirect_uri=http%3A%2F%2Flocalhost%3A80%2Fcallback",
		"https://x.example/a?redirect_uri=http%3A%2F%2Flocalhost%3A99999%2Fcallback",
	} {
		t.Run(target, func(t *testing.T) {
			if classification := ClassifyTarget(target); classification.Verdict != VerdictRefuse {
				t.Fatalf("ClassifyTarget = %+v, want refuse", classification)
			}
		})
	}
}

// TestClassifyTargetBoundsTargetLength pins the length limit with literal
// boundaries. A MaxTargetBytes-relative expectation moves with the mutated
// constant, so it can never disagree with it.
func TestClassifyTargetBoundsTargetLength(t *testing.T) {
	prefix := "https://example.com/"
	atLimit := prefix + strings.Repeat("a", 8192-len(prefix))
	if len(atLimit) != 8192 {
		t.Fatalf("built target of length %d, want 8192", len(atLimit))
	}
	if classification := ClassifyTarget(atLimit); classification.Verdict != VerdictOpen {
		t.Fatalf("ClassifyTarget at the limit = %+v, want open", classification)
	}
	overLimit := prefix + strings.Repeat("a", 8193-len(prefix))
	if classification := ClassifyTarget(
		overLimit,
	); classification.Verdict != VerdictRefuse ||
		!strings.Contains(classification.Reason, "too long") {
		t.Fatalf("ClassifyTarget over the limit = %+v, want refuse too long", classification)
	}
}

// TestClassifyTargetBoundsLoopbackRedirectPorts pins the redirect port range
// with literal boundaries.
func TestClassifyTargetBoundsLoopbackRedirectPorts(t *testing.T) {
	for _, testCase := range []struct {
		port int
	}{{1024}, {65535}} {
		target := "https://x.example/a?redirect_uri=http%3A%2F%2Flocalhost%3A" + strconv.Itoa(
			testCase.port,
		) + "%2Fcallback"
		classification := ClassifyTarget(target)
		if classification.Verdict != VerdictLoopbackRedirect || classification.Redirect.Port != testCase.port {
			t.Fatalf("ClassifyTarget(port %d) = %+v, want loopback redirect", testCase.port, classification)
		}
	}
	for _, testCase := range []struct {
		port int
	}{{1023}, {65536}} {
		target := "https://x.example/a?redirect_uri=http%3A%2F%2Flocalhost%3A" + strconv.Itoa(
			testCase.port,
		) + "%2Fcallback"
		classification := ClassifyTarget(target)
		if classification.Verdict != VerdictRefuse || !strings.Contains(classification.Reason, "no usable port") {
			t.Fatalf("ClassifyTarget(port %d) = %+v, want refuse no usable port", testCase.port, classification)
		}
	}
}

// TestClassifyTargetRefusesUnparseableURL covers url.Parse failing after the
// control-character check: the failure must be refused, not dereferenced.
func TestClassifyTargetRefusesUnparseableURL(t *testing.T) {
	if classification := ClassifyTarget("https://[::1"); classification.Verdict != VerdictRefuse {
		t.Fatalf("ClassifyTarget = %+v, want refuse", classification)
	}
}

// TestClassifyTargetAmbiguousNumericHostReason pins the reason, not just the
// verdict, so the refusal cannot degrade to an empty classification.
func TestClassifyTargetAmbiguousNumericHostReason(t *testing.T) {
	classification := ClassifyTarget("http://127.1:8080/")
	if classification.Verdict != VerdictRefuse || !strings.Contains(classification.Reason, "numeric host") {
		t.Fatalf("ClassifyTarget = %+v, want refuse mentioning the numeric host", classification)
	}
}

// TestClassifyTargetRedirectParameterHandling covers the redirect query walk:
// a malformed value, a non-web scheme, a non-loopback host, and an earlier
// parameter that must not stop the search for a later one.
func TestClassifyTargetRedirectParameterHandling(t *testing.T) {
	for _, target := range []string{
		"https://x.example/a?redirect_uri=ftp%3A%2F%2Flocalhost%3A1024%2Fcb",
		"https://x.example/a?redirect_uri=http%3A%2F%2Flocalhost%3A1024%2F%25zz",
		"https://x.example/a?redirect_uri=https%3A%2F%2Fexample.com%3A1024%2Fcb",
	} {
		t.Run(target, func(t *testing.T) {
			if classification := ClassifyTarget(target); classification.Verdict != VerdictOpen {
				t.Fatalf("ClassifyTarget = %+v, want open", classification)
			}
		})
	}

	later := "https://x.example/a?redirect_uri=https%3A%2F%2Fexample.com%2Fx&redirect_url=http%3A%2F%2Flocalhost%3A1024%2Fcb"
	classification := ClassifyTarget(later)
	if classification.Verdict != VerdictLoopbackRedirect || classification.Redirect.Port != 1024 {
		t.Fatalf("ClassifyTarget = %+v, want the later loopback redirect", classification)
	}
}

func TestIsWebSchemeLowercases(t *testing.T) {
	for _, scheme := range []string{"http", "https", "HTTP", "HTTPS", "HtTpS"} {
		if !isWebScheme(scheme) {
			t.Fatalf("isWebScheme(%q) = false, want true", scheme)
		}
	}
	for _, scheme := range []string{"ftp", "file", ""} {
		if isWebScheme(scheme) {
			t.Fatalf("isWebScheme(%q) = true, want false", scheme)
		}
	}
}

// TestIsAmbiguousNumericHostNormalizesHost covers the trailing-dot trim and the
// colon guard, which the URL-level cases cannot reach.
func TestIsAmbiguousNumericHostNormalizesHost(t *testing.T) {
	if !isAmbiguousNumericHost("127.1.") {
		t.Fatal("isAmbiguousNumericHost(\"127.1.\") = false, want true")
	}
	if isAmbiguousNumericHost("fe80::1.2") {
		t.Fatal("isAmbiguousNumericHost(\"fe80::1.2\") = true, want false")
	}
}

// TestIsControlCharacterBoundaries pins the control-character limits with
// literal code points.
func TestIsControlCharacterBoundaries(t *testing.T) {
	for _, testCase := range []struct {
		character rune
		want      bool
	}{{0x1f, true}, {0x20, false}, {0x7e, false}, {0x7f, true}} {
		if got := isControlCharacter(testCase.character); got != testCase.want {
			t.Fatalf("isControlCharacter(%#x) = %v, want %v", testCase.character, got, testCase.want)
		}
	}
}
