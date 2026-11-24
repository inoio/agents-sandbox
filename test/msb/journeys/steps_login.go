package journeys

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// loginFacts returns the harness's login facts, or an error naming a harness
// the login journey cannot cover yet.
func loginFacts(c *Context) (LoginFacts, error) {
	if c.Harness.Login == nil {
		return LoginFacts{}, fmt.Errorf("the %s adapter declares no login facts", c.Agent)
	}
	return *c.Harness.Login, nil
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
			nothing := func() error {
				return fmt.Errorf("the host's browser was asked to open nothing new; output: %s", ses.OutputTail())
			}
			return pollUntil(scaled(loginLinkTimeout), stubPollInterval, func() (bool, error) {
				links, err := c.Browser.Opened()
				if err != nil {
					return false, err
				}
				if len(links) <= c.Browser.taken {
					if ses.HasExited() {
						return false, nothing()
					}
					return false, nil
				}
				login, err := parseLoginLink(links[c.Browser.taken])
				if err != nil {
					return false, err
				}
				c.Browser.taken++
				c.Login = login
				return true, nil
			}, nothing)
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
			scaled(loginCallbackTimeout),
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
		if problem := linkOriginProblem(c.Login.Link, c.OAuth.Origin()); problem != nil {
			return problem
		}
		timeout := scaled(loginCallbackTimeout)
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
		_, err = ses.WaitExit(scaled(loginExitTimeout))
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
