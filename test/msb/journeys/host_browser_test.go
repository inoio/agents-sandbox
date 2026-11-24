package journeys

import (
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestParseLoginLinkNeedsALoopbackCallback(t *testing.T) {
	const callback = "http://localhost:1455/auth/callback"
	link := "https://auth.example.test/oauth/authorize?" + url.Values{
		"redirect_uri": {callback},
		"state":        {"the-state"},
	}.Encode()
	login, err := parseLoginLink(link)
	if err != nil {
		t.Fatalf("parseLoginLink(%q): %v", link, err)
	}
	if login.Callback.String() != callback || login.State != "the-state" || login.Link != link {
		t.Errorf("login = %+v, want the callback %s and the state", login, callback)
	}

	for name, bad := range map[string]string{
		"not a web link":        "mailto:someone@example.test?redirect_uri=" + url.QueryEscape(callback),
		"no redirect_uri":       "https://auth.example.test/oauth/authorize",
		"a remote callback":     "https://auth.example.test/?redirect_uri=" + url.QueryEscape("http://example.test:1455/cb"),
		"a callback on no port": "https://auth.example.test/?redirect_uri=" + url.QueryEscape("http://127.0.0.1/cb"),
	} {
		if _, err := parseLoginLink(bad); err == nil {
			t.Errorf("%s: parseLoginLink(%q) = nil error, want a refusal", name, bad)
		}
	}
}

func TestCallbackURLReturnsToTheHostLoopbackWithTheState(t *testing.T) {
	callback, _ := url.Parse("http://localhost:1455/auth/callback")
	got, err := url.Parse(callbackURL(&LoginLink{Callback: callback, State: "the-state"}))
	if err != nil {
		t.Fatal(err)
	}
	query := got.Query()
	if got.Host != "127.0.0.1:1455" || got.Path != "/auth/callback" ||
		query.Get("state") != "the-state" || query.Get("code") != loginCallbackCode {
		t.Errorf("callback URL = %s, want the host loopback port 1455 with the state and code", got)
	}
}

func TestCallbackAnswerProblemTellsTheHarnesssAnswerFromTheForwarders(t *testing.T) {
	const marker = "Codex Sign-in"
	relayed := http.Header{"Content-Security-Policy": {"sandbox; default-src 'none'"}}
	page := "<title>Codex Sign-in Error</title>"
	if problem := callbackAnswerProblem(
		&CallbackAnswer{Status: 200, Header: relayed, Body: page},
		marker,
	); problem != nil {
		t.Errorf("the harness's relayed page: %v", problem)
	}
	for name, answer := range map[string]*CallbackAnswer{
		"the forwarder's failed replay": {Status: http.StatusBadGateway, Body: "agents-sandbox could not pass the login back"},
		"an unknown path":               {Status: http.StatusNotFound, Body: "404 page not found"},
		"a blocked redirect": {
			Status: 200, Header: relayed,
			Body: "agents-sandbox blocked a redirect to a local address on your computer.",
		},
		"no sandboxing policy": {Status: 200, Header: http.Header{}, Body: page},
		"a cookie": {Status: 200, Body: page, Header: http.Header{
			"Content-Security-Policy": {"sandbox"},
			"Set-Cookie":              {"session=1"},
		}},
	} {
		if callbackAnswerProblem(answer, marker) == nil {
			t.Errorf("%s: got no problem", name)
		}
	}
	if callbackAnswerProblem(&CallbackAnswer{Status: 200, Header: relayed, Body: page}, "") == nil {
		t.Error("an adapter without a callback marker: got no problem")
	}
}

func TestHostBrowserRecordsTheLinksItIsAskedToOpen(t *testing.T) {
	browser, err := newHostBrowser(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if opened, err := browser.Opened(); err != nil || len(opened) != 0 {
		t.Fatalf("before any link: Opened() = %v, %v; want none", opened, err)
	}
	links := []string{"https://example.test/a?x=1&y=2", "https://example.test/it's"}
	env := browser.FirstOnPath([]string{"PATH=/usr/bin:/bin", "HOME=/nowhere"})
	if !slices.Contains(env, "PATH="+browser.dir+string(os.PathListSeparator)+"/usr/bin:/bin") {
		t.Fatalf("env = %v, want the browser's dir first on PATH", env)
	}
	command := hostOpenCommand(runtime.GOOS)
	resolved, err := lookPathIn(command, env)
	if err != nil || resolved != filepath.Join(browser.dir, command) {
		t.Fatalf("%s on the journey PATH resolves to %q (%v), want the fake browser", command, resolved, err)
	}
	for _, link := range links {
		//testenv:allow running the fake handler the launcher would run
		command := exec.CommandContext(t.Context(), resolved, link)
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("%s %q: %v\n%s", resolved, link, err, out)
		}
	}
	opened, err := browser.Opened()
	if err != nil || !slices.Equal(opened, links) {
		t.Errorf("Opened() = %v, %v; want %v", opened, err, links)
	}
	if env := browser.FirstOnPath([]string{"HOME=/nowhere"}); !strings.HasPrefix(env[len(env)-1], "PATH="+browser.dir) {
		t.Errorf("without a PATH: env = %v, want one naming the browser's dir", env)
	}
}

func TestHostOpenCommandFollowsTheLaunchersOpener(t *testing.T) {
	if hostOpenCommand("darwin") != "open" || hostOpenCommand("linux") != "xdg-open" {
		t.Errorf("hostOpenCommand = %q (darwin), %q (linux); want open and xdg-open",
			hostOpenCommand("darwin"), hostOpenCommand("linux"))
	}
}

// lookPathIn resolves command against the PATH in env, as the launcher's
// exec.Command does with its own environment.
func lookPathIn(command string, env []string) (string, error) {
	for _, entry := range env {
		if path, ok := strings.CutPrefix(entry, "PATH="); ok {
			for _, dir := range filepath.SplitList(path) {
				candidate := filepath.Join(dir, command)
				if info, err := os.Stat(candidate); err == nil && info.Mode()&0o111 != 0 {
					return candidate, nil
				}
			}
		}
	}
	return "", os.ErrNotExist
}

func TestLinkOriginProblemAcceptsOnlyTheStubsOrigin(t *testing.T) {
	const origin = "http://host.microsandbox.internal:4242"
	if problem := linkOriginProblem(origin+"/authorize?x=1", origin); problem != nil {
		t.Errorf("the stub's link: %v", problem)
	}
	for _, link := range []string{
		"https://auth.openai.com/oauth/authorize",
		"http://host.microsandbox.internal:4243/authorize",
		"https://host.microsandbox.internal:4242/authorize",
	} {
		if linkOriginProblem(link, origin) == nil {
			t.Errorf("linkOriginProblem(%q) = nil, want a refusal", link)
		}
	}
}

func TestOnHostLoopbackKeepsTheCallback(t *testing.T) {
	target, _ := url.Parse("http://localhost:34219/callback/x?code=c&state=s")
	if got := onHostLoopback(target); got != "http://127.0.0.1:34219/callback/x?code=c&state=s" {
		t.Errorf("onHostLoopback = %s", got)
	}
}
