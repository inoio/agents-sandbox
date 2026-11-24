package journeys

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/test/msb/driver"
)

// hostOpenCommand is the command the launcher's host opener runs on goos
// (open on macOS, xdg-open elsewhere); the fake browser takes its place on
// PATH, so the journey never opens a real browser.
func hostOpenCommand(goos string) string {
	if goos == "darwin" {
		return "open"
	}
	return "xdg-open"
}

// loginCallbackCode is the authorization code the fake browser returns with.
// No provider issued it, so the harness's token exchange fails; the journey
// asserts the hand-over, not the login.
const loginCallbackCode = "journeys-not-a-real-code"

// Timeouts for the login hand-over. The link waits on the VM start; the
// callback on the harness's token exchange, which the forwarder bounds at two
// minutes.
const (
	loginLinkTimeout     = time.Minute
	loginCallbackTimeout = 150 * time.Second
	loginCallbackDial    = 10 * time.Second
)

// HostBrowser is a fake host link handler: an executable named like the host
// opener, first on the journey launcher's PATH, that records each link it is
// asked to open instead of opening it.
type HostBrowser struct {
	dir string
	log string
	// taken is how many recorded links a step has already taken.
	taken int
}

// newHostBrowser writes the fake handler into dir.
func newHostBrowser(dir string) (*HostBrowser, error) {
	browser := &HostBrowser{dir: dir, log: filepath.Join(dir, "opened.log")}
	script := "#!/bin/sh\nprintf '%s\\n' \"$1\" >> " + shellQuote(browser.log) + "\n"
	if err := os.WriteFile(filepath.Join(dir, hostOpenCommand(runtime.GOOS)), []byte(script), 0o755); err != nil {
		return nil, fmt.Errorf("write the fake host browser: %w", err)
	}
	return browser, nil
}

// FirstOnPath returns env with the fake handler's dir first on PATH.
func (b *HostBrowser) FirstOnPath(env []string) []string {
	out := make([]string, 0, len(env)+1)
	found := false
	for _, entry := range env {
		if value, ok := strings.CutPrefix(entry, "PATH="); ok {
			entry = "PATH=" + b.dir + string(os.PathListSeparator) + value
			found = true
		}
		out = append(out, entry)
	}
	if !found {
		out = append(out, "PATH="+b.dir)
	}
	return out
}

// Opened returns the links the handler was asked to open, in order.
func (b *HostBrowser) Opened() ([]string, error) {
	file, err := os.Open(b.log)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var links []string
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		if line := strings.TrimSpace(scanner.Text()); line != "" {
			links = append(links, line)
		}
	}
	return links, scanner.Err()
}

// loginFacts returns the harness's login facts, or an error naming a harness
// the login journey cannot cover yet.
func loginFacts(c *Context) (LoginFacts, error) {
	if c.Harness.Login == nil {
		return LoginFacts{}, fmt.Errorf("the %s adapter declares no login facts", c.Agent)
	}
	return *c.Harness.Login, nil
}

// LoginLink is a harness's authorization link and the loopback callback in
// the VM it names.
type LoginLink struct {
	Link     string
	Callback *url.URL
	State    string
}

// parseLoginLink reads link as an http(s) authorization URL whose redirect_uri
// is a loopback http callback with a port.
func parseLoginLink(link string) (*LoginLink, error) {
	parsed, err := url.Parse(link)
	if err != nil {
		return nil, fmt.Errorf("the opened link %q is not a URL: %w", link, err)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return nil, fmt.Errorf("the opened link %q is not a web authorization URL", link)
	}
	query := parsed.Query()
	callback, err := url.Parse(query.Get("redirect_uri"))
	if err != nil || callback.Scheme != "http" || callback.Port() == "" || !isLoopbackHost(callback.Hostname()) {
		return nil, fmt.Errorf("the opened link %q names no loopback redirect_uri with a port", link)
	}
	return &LoginLink{Link: link, Callback: callback, State: query.Get("state")}, nil
}

// isLoopbackHost reports whether host names the loopback interface.
func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// CallbackAnswer is what the host's callback forwarder answered the browser.
type CallbackAnswer struct {
	Status int
	Header http.Header
	Body   string
}

// StepFakeHostBrowser puts a fake host link handler first on the journey
// launcher's PATH, standing in for the user's browser. It lives beside the
// journey's isolated HOME, so StepFreshHome must run first.
func StepFakeHostBrowser() Step {
	return Step{Name: "the host's browser is a recorder", Run: func(c *Context) error {
		dir := filepath.Join(c.Home, "host-browser")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create the fake host browser's dir: %w", err)
		}
		browser, err := newHostBrowser(dir)
		if err != nil {
			return err
		}
		c.Browser = browser
		return nil
	}}
}

// StepLaunchLogin starts the harness's browser login (the adapter's
// LoginArgs) under `agents-sandbox run`.
func StepLaunchLogin() Step {
	return Step{Name: "the user starts the harness's browser login", Run: func(c *Context) error {
		login, err := loginFacts(c)
		if err != nil {
			return err
		}
		launchClient(c, []string{runCommand}, login.BrowserArgs)
		return nil
	}}
}

// StepAssertLoginLinkOpened waits until the host browser was asked to open a
// new link (one no earlier step took) and checks it is an authorization link
// naming a loopback callback.
func StepAssertLoginLinkOpened() Step {
	return Step{
		Name: "the host's browser opens the login link, which names a callback in the VM",
		Run: func(c *Context) error {
			if c.Browser == nil {
				return errors.New("no fake host browser on the context; set it up first")
			}
			ses, err := session(c)
			if err != nil {
				return err
			}
			deadline := time.Now().Add(driver.ScaleTimeout(loginLinkTimeout, driver.SmokeTimeoutFactor()))
			for {
				links, err := c.Browser.Opened()
				if err != nil {
					return err
				}
				if len(links) > c.Browser.taken {
					login, err := parseLoginLink(links[c.Browser.taken])
					if err != nil {
						return err
					}
					c.Browser.taken++
					c.Login = login
					return nil
				}
				if ses.HasExited() || !time.Now().Before(deadline) {
					return fmt.Errorf("the host's browser was asked to open nothing; output: %s", ses.OutputTail())
				}
				//testenv:allow polling the fake browser's record
				time.Sleep(stubPollInterval)
			}
		},
	}
}

// StepBrowserReturnsToCallback does what the browser does after a sign-in: it
// requests the callback on the host's loopback with the link's state and a
// code, and keeps the host forwarder's answer.
func StepBrowserReturnsToCallback() Step {
	return Step{Name: "the browser returns to the callback after the sign-in", Run: func(c *Context) error {
		if c.Login == nil {
			return errors.New("no login link on the context; wait for it first")
		}
		answer, err := returnToCallback(
			callbackURL(c.Login),
			driver.ScaleTimeout(loginCallbackTimeout, driver.SmokeTimeoutFactor()),
		)
		if err != nil {
			return err
		}
		c.Callback = answer
		c.TB.Logf(
			"the forwarder answered %d (%s): %.300q",
			answer.Status,
			answer.Header.Get("Content-Type"),
			answer.Body,
		)
		return nil
	}}
}

// StepBrowserSignsIn does what the browser does with the opened link when the
// user signs in at an authorization server the journey runs: it opens the link
// (resolving the VM's name for the host, under which the stub's authorize
// endpoint is advertised, to the host loopback), takes the server's redirect,
// and follows it to the callback on the host's loopback, keeping the host
// forwarder's answer.
func StepBrowserSignsIn() Step {
	return Step{Name: "the user signs in at the authorization server in the browser", Run: func(c *Context) error {
		if c.Login == nil || c.OAuth == nil {
			return errors.New("no login link or stub authorization server on the context")
		}
		timeout := driver.ScaleTimeout(loginCallbackTimeout, driver.SmokeTimeoutFactor())
		back, err := authorizeRedirect(c.Login.Link, c.OAuth.LoopbackAddr(), timeout)
		if err != nil {
			return err
		}
		answer, err := returnToCallback(onHostLoopback(back), timeout)
		if err != nil {
			return err
		}
		c.Callback = answer
		c.TB.Logf(
			"the forwarder answered %d (%s): %.300q",
			answer.Status,
			answer.Header.Get("Content-Type"),
			answer.Body,
		)
		return nil
	}}
}

// authorizeRedirect opens link with the browser's request, dialing loopback for
// every host (the stub's authorize endpoint is the link's only host), and
// returns where the authorization server sends the browser back.
func authorizeRedirect(link, loopback string, timeout time.Duration) (*url.URL, error) {
	client := &http.Client{
		Timeout:       timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport:     &http.Transport{Proxy: nil, DialContext: loopbackDialer(loopback)},
	}
	//nolint:noctx // the client's timeout bounds the request
	response, err := client.Get(link)
	if err != nil {
		return nil, fmt.Errorf("open the authorization link: %w", err)
	}
	defer response.Body.Close()
	location, err := response.Location()
	if err != nil {
		return nil, fmt.Errorf("the authorization server answered %d without a redirect: %w", response.StatusCode, err)
	}
	return location, nil
}

// loopbackDialer dials loopback whatever address a request names: the
// browser's resolution of the VM's name for the host to the host itself.
func loopbackDialer(loopback string) func(ctx context.Context, network, address string) (net.Conn, error) {
	return func(ctx context.Context, network, _ string) (net.Conn, error) {
		return new(net.Dialer).DialContext(ctx, network, loopback)
	}
}

// onHostLoopback is target with its host replaced by the host's IPv4 loopback,
// keeping the port, path, and query: where a browser lands for a localhost
// callback.
func onHostLoopback(target *url.URL) string {
	landed := *target
	landed.Host = net.JoinHostPort("127.0.0.1", target.Port())
	return landed.String()
}

// callbackURL is the browser's return address for login: the callback on the
// host's IPv4 loopback with the link's state and loginCallbackCode.
func callbackURL(login *LoginLink) string {
	query := url.Values{"code": {loginCallbackCode}}
	if login.State != "" {
		query.Set("state", login.State)
	}
	target := url.URL{
		Scheme:   "http",
		Host:     net.JoinHostPort("127.0.0.1", login.Callback.Port()),
		Path:     login.Callback.Path,
		RawQuery: query.Encode(),
	}
	return target.String()
}

// returnToCallback requests target without following redirects, retrying a
// refused connection briefly in case the forward is still being set up.
func returnToCallback(target string, timeout time.Duration) (*CallbackAnswer, error) {
	client := &http.Client{
		Timeout:       timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	deadline := time.Now().Add(loginCallbackDial)
	for {
		//nolint:noctx // the client's timeout bounds the request
		response, err := client.Get(target)
		if err == nil {
			defer response.Body.Close()
			body, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
			if readErr != nil {
				return nil, fmt.Errorf("read the callback answer: %w", readErr)
			}
			return &CallbackAnswer{Status: response.StatusCode, Header: response.Header, Body: string(body)}, nil
		}
		if !time.Now().Before(deadline) {
			return nil, fmt.Errorf("the host forwards no callback on %s: %w", target, err)
		}
		//testenv:allow waiting for the host forward to listen
		time.Sleep(stubPollInterval)
	}
}

// StepAssertCallbackReachedHarness checks the forwarder passed the browser's
// callback to the harness in the VM and relayed the harness's own page (the
// adapter's Login.CallbackMarker) under the host boundary: a sandboxing
// Content-Security-Policy and no cookies.
func StepAssertCallbackReachedHarness() Step {
	return AsCheck(
		Step{Name: "the callback reaches the harness in the VM, behind the host boundary", Run: func(c *Context) error {
			if c.Callback == nil {
				return errors.New("no callback answer on the context")
			}
			login, err := loginFacts(c)
			if err != nil {
				return err
			}
			return callbackAnswerProblem(c.Callback, login.CallbackMarker)
		}},
	)
}

// callbackAnswerProblem reports why answer is not the harness's own page
// (showing marker) relayed under the host boundary, or nil. An answer the
// forwarder writes itself (a 404, a failed replay, a blocked redirect) lacks
// the harness's marker.
func callbackAnswerProblem(answer *CallbackAnswer, marker string) error {
	if marker == "" {
		return errors.New("the adapter declares no LoginCallbackMarker")
	}
	if !strings.Contains(answer.Body, marker) {
		return fmt.Errorf(
			"the forwarder answered %d without the harness's page (%q): %.300q",
			answer.Status,
			marker,
			answer.Body,
		)
	}
	if policy := answer.Header.Get("Content-Security-Policy"); !strings.Contains(policy, "sandbox") {
		return fmt.Errorf("the relayed answer carries no sandboxing Content-Security-Policy (%q)", policy)
	}
	if cookies := answer.Header.Values("Set-Cookie"); len(cookies) > 0 {
		return fmt.Errorf("the relayed answer sets cookies on the host: %q", cookies)
	}
	return nil
}

// loginExitTimeout bounds a login command's exit once its outcome is decided.
const loginExitTimeout = time.Minute

// signInSettle is how long StepAssertNoSignIn lets the start screen settle: a
// sign-in screen is painted over the composer once the harness checked for a
// stored login.
const signInSettle = 10 * time.Second

// StepWaitLoginEnds waits until the active client's login command exited,
// whatever its outcome.
func StepWaitLoginEnds() Step {
	return Step{Name: "the login command ends", Run: func(c *Context) error {
		ses, err := session(c)
		if err != nil {
			return err
		}
		_, err = ses.WaitExit(driver.ScaleTimeout(loginExitTimeout, driver.SmokeTimeoutFactor()))
		return err
	}}
}

// StepSignInWithAPIKey signs the harness in from `agents-sandbox shell` with
// its own API-key login (the adapter's Login.APIKey), so the harness writes its
// login store in the VM's home. The store is not provisioned, so only the home
// volume keeps it.
func StepSignInWithAPIKey() Step {
	return Step{Name: "the user signs the harness in with an API key from the shell", Run: func(c *Context) error {
		login, err := loginFacts(c)
		if err != nil {
			return err
		}
		code, ses, err := runInShell(c, login.APIKey(stubProviderAPIKey))
		if err != nil {
			return err
		}
		if code != 0 {
			return fmt.Errorf("the API-key login exited %d; output: %s", code, ses.OutputTail())
		}
		return nil
	}}
}

// keyRetypeInterval is how often typeUntilExit types its input again. The
// launcher's attach takes the terminal only once the VM is up, so input typed
// earlier can be lost.
const keyRetypeInterval = 2 * time.Second

// typeUntilExit types input into ses until the process exits or budget
// elapses. The input must end the process (a shell line ending in exit), so a
// repeat only runs once more when the first was lost.
func typeUntilExit(ses *driver.Session, input []byte, budget time.Duration) error {
	deadline := time.Now().Add(budget)
	for !ses.HasExited() {
		if !time.Now().Before(deadline) {
			return fmt.Errorf("the login did not finish within %s; output: %s", budget, ses.OutputTail())
		}
		if err := ses.Write(input); err != nil {
			return fmt.Errorf("type the login input: %w", err)
		}
		//testenv:allow the retype cadence covers input lost before the attach
		time.Sleep(keyRetypeInterval)
	}
	return nil
}

// StepAssertLoginStored asks the harness, from `agents-sandbox shell`, whether
// a login is stored (the adapter's Login.Status command, which exits 0 only
// then): the positive evidence beside the start screen's absent sign-in.
func StepAssertLoginStored() Step {
	return AsCheck(Step{Name: "the harness reports the stored login from the shell", Run: func(c *Context) error {
		login, err := loginFacts(c)
		if err != nil {
			return err
		}
		code, ses, err := runInShell(c, login.Status)
		if err != nil {
			return err
		}
		if code != 0 {
			return fmt.Errorf(
				"%s exited %d: no login is stored; output: %s",
				login.Status,
				code,
				ses.OutputTail(),
			)
		}
		return nil
	}})
}

// runInShell types command into a new `agents-sandbox shell` client, ending
// the shell with the command's status, and returns that status.
func runInShell(c *Context, command string) (int, *driver.Session, error) {
	launchClient(c, []string{"shell"}, nil)
	ses := c.Session
	budget := driver.ScaleTimeout(loginExitTimeout, driver.SmokeTimeoutFactor())
	if err := typeUntilExit(ses, []byte(command+"; exit\r"), budget); err != nil {
		return 0, ses, err
	}
	code, err := ses.WaitExit(budget)
	return code, ses, err
}

// StepStartStubOAuthServer starts the stub authorization server with its MCP
// server, and declares that MCP server in the harness's config through the
// adapter's Login.MCPServer snippets.
func StepStartStubOAuthServer() Step {
	return Step{
		Name: "an MCP server behind an OAuth authorization server is in the harness's config",
		Run: func(c *Context) error {
			login, err := loginFacts(c)
			if err != nil {
				return err
			}
			stub, err := StartStubOAuthServer(c.TB)
			if err != nil {
				return err
			}
			c.OAuth = stub
			profile, ok := agent.Lookup(c.Agent)
			if !ok {
				return fmt.Errorf("unknown agent %q", c.Agent)
			}
			return driver.SeedAgentSnippets(
				c.Home,
				profile.ConfigDirName(),
				login.MCPServer(stubMCPServerName, stub.MCPURL()),
			)
		},
	}
}

// StepLaunchMCPLogin starts the harness's sign-in to the stub's MCP server (the
// adapter's Login.MCPLoginArgs) under `agents-sandbox run`.
func StepLaunchMCPLogin() Step {
	return Step{Name: "the user signs the harness in to the MCP server", Run: func(c *Context) error {
		login, err := loginFacts(c)
		if err != nil {
			return err
		}
		launchClient(c, []string{runCommand}, login.MCPLoginArgs(stubMCPServerName))
		return nil
	}}
}

// StepAssertMCPLoginCompleted checks the MCP sign-in finished: the login
// command exited 0 and the stub's token endpoint issued a token, which it does
// only for a code whose PKCE verifier matches.
func StepAssertMCPLoginCompleted() Step {
	return Step{Name: "the MCP sign-in completes with a token exchange in the VM", Run: func(c *Context) error {
		ses, err := session(c)
		if err != nil {
			return err
		}
		code, err := ses.WaitExit(driver.ScaleTimeout(loginExitTimeout, driver.SmokeTimeoutFactor()))
		if err != nil {
			return err
		}
		if code != 0 {
			return fmt.Errorf("the MCP sign-in exited %d; output: %s", code, ses.OutputTail())
		}
		if len(c.OAuth.Issued()) == 0 {
			return fmt.Errorf("the stub issued no token (requests: %+v)", c.OAuth.Requests())
		}
		// Count only the calls after the sign-in, so a later check sees the
		// stored login in use, not the sign-in's own call.
		c.TB.Logf("authorized MCP calls during the sign-in: %d", c.OAuth.ResetAuthorizedMCPCalls())
		return nil
	}}
}

// mcpCallTimeout bounds how long the harness may take to call its MCP server
// after it started.
const mcpCallTimeout = 30 * time.Second

// StepAssertMCPCalledWithToken checks the harness called the stub's MCP server
// with a token the stub issued since the sign-in completed: the stored MCP
// login is in use.
func StepAssertMCPCalledWithToken() Step {
	return AsCheck(Step{Name: "the harness calls the MCP server with the issued token", Run: func(c *Context) error {
		deadline := time.Now().Add(driver.ScaleTimeout(mcpCallTimeout, driver.SmokeTimeoutFactor()))
		for c.OAuth.AuthorizedMCPCalls() == 0 {
			if !time.Now().Before(deadline) {
				return fmt.Errorf("no MCP call carried an issued token (requests: %+v)", c.OAuth.Requests())
			}
			//testenv:allow polling the stub for the harness's MCP call
			time.Sleep(stubPollInterval)
		}
		return nil
	}})
}

// StepAssertNoSignIn checks the active client's harness started without its
// sign-in screen (the adapter's Login.SignInMarker): a login is stored.
func StepAssertNoSignIn() Step {
	return AsCheck(Step{Name: "the harness starts signed in", Run: func(c *Context) error {
		login, err := loginFacts(c)
		if err != nil {
			return err
		}
		ses, err := session(c)
		if err != nil {
			return err
		}
		//testenv:allow letting a sign-in screen paint over the composer
		time.Sleep(signInSettle)
		if firstShownMarker(string(ses.Output()), []string{login.SignInMarker}) >= 0 {
			return errors.New("the harness shows its sign-in screen; the stored login was lost")
		}
		return nil
	}})
}

// loginJourney drives a harness's logins through the sandbox, with no real
// account:
//
//   - The browser login goes up to the provider: the link reaches the host's
//     browser, and the browser's return to the loopback callback reaches the
//     harness in the VM through the host forwarder. No provider is involved,
//     so the harness's token exchange fails; the journey asserts the sandbox's
//     part.
//   - An API-key login from the shell stores a login in the VM's home.
//   - The MCP sign-in runs a full OAuth login against the stub authorization
//     server on the host: discovery, client registration, the browser's
//     authorize and callback through the opener and the forwarder, and the
//     token exchange with PKCE from the VM.
//   - Both stored logins must survive a VM stop: the harness starts signed in
//     and calls the MCP server with the issued token.
//
// It runs on its own project, so the stored logins stay out of the harness's
// shared home volume, and under the `host` profile, which admits only the host
// gateway the stub listens behind: no step reaches a real provider.
var loginJourney = Journey{
	Name:      "harness-login",
	Harnesses: []string{codexAgentName},
	Network:   hostGatewayNetwork,
	Steps: []Step{
		Given(StepOwnProject("login")),
		And(StepFreshHome()),
		And(StepConfigureLauncher()),
		And(StepFakeHostBrowser()),
		And(StepStartStubOAuthServer()),
		When(StepLaunchLogin()),
		Then(StepAssertLoginLinkOpened()),
		When(StepBrowserReturnsToCallback()),
		Then(StepAssertCallbackReachedHarness()),
		And(StepWaitLoginEnds()),
		When(StepSignInWithAPIKey()),
		And(StepLaunchMCPLogin()),
		Then(StepAssertLoginLinkOpened()),
		When(StepBrowserSignsIn()),
		Then(StepAssertMCPLoginCompleted()),
		When(StepStopVM()),
		Then(StepAssertLoginStored()),
		When(StepLaunch(runCommand)),
		Then(StepWaitReady()),
		And(StepAssertNoSignIn()),
		And(StepAssertMCPCalledWithToken()),
		When(StepQuitToExit()),
	},
}
