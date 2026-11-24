package journeys

import (
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
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
		"not https":             "http://auth.example.test/?redirect_uri=" + url.QueryEscape(callback),
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
	relayed := http.Header{"Content-Security-Policy": {"sandbox; default-src 'none'"}}
	if problem := callbackAnswerProblem(&CallbackAnswer{Status: 200, Header: relayed}); problem != nil {
		t.Errorf("a relayed answer: %v", problem)
	}
	for name, answer := range map[string]*CallbackAnswer{
		"the forwarder's failed replay": {Status: http.StatusBadGateway, Header: relayed},
		"an unknown path":               {Status: http.StatusNotFound, Header: relayed},
		"no sandboxing policy":          {Status: 200, Header: http.Header{}},
		"a cookie": {Status: 200, Header: http.Header{
			"Content-Security-Policy": {"sandbox"},
			"Set-Cookie":              {"session=1"},
		}},
	} {
		if callbackAnswerProblem(answer) == nil {
			t.Errorf("%s: got no problem", name)
		}
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
	for _, link := range links {
		//testenv:allow running the fake handler the launcher would run
		command := exec.CommandContext(t.Context(), filepath.Join(browser.dir, hostOpenCommand), link)
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("%s %q: %v\n%s", hostOpenCommand, link, err, out)
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
