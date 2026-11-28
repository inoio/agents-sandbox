package journeys

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
)

func TestBrowserCallbackStepSendsTheProviderSpecificParameters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("client_id") != "journeys-client" ||
			request.URL.Query().Get("state") != "state" || request.URL.Query().Get("code") != loginCallbackCode {
			http.Error(writer, "invalid callback", http.StatusBadRequest)
			return
		}
		_, _ = writer.Write([]byte("callback accepted"))
	}))
	t.Cleanup(server.Close)
	callback, err := url.Parse(server.URL + "/auth/callback")
	if err != nil {
		t.Fatal(err)
	}
	c := &Context{
		TB: t, Agent: piAgentName, Harness: Harnesses[piAgentName],
		Login: &LoginLink{Callback: callback, State: "state"},
	}
	if err := StepBrowserReturnsToCallback().Run(c); err != nil {
		t.Fatal(err)
	}
	if c.Callback.Status != http.StatusOK || c.Callback.Body != "callback accepted" {
		t.Fatalf("callback = %+v, want the provider's accepted response", c.Callback)
	}
}

func TestStepLaunchLoginUsesShellWithoutTUIArguments(t *testing.T) {
	for name, command := range map[string]string{
		codexAgentName:      "codex login",
		claudeCodeAgentName: "claude auth login",
		opencode2AgentName:  "opencode2 auth login openai --method chatgpt-browser --server http://127.0.0.1:4096",
		piAgentName:         "pi auth login openai",
		ohMyPiAgentName:     "omp login openai-codex",
	} {
		t.Run(name, func(t *testing.T) {
			c := shellLoginContext(t, name, command)
			if err := StepLaunchLogin().Run(c); err != nil {
				t.Fatal(err)
			}
			assertShellLoginExited(t, c)
		})
	}
}

func TestAPIKeyLoginStepSkipsAdaptersWithoutAnAPIKeyLogin(t *testing.T) {
	c := &Context{TB: t, Agent: piAgentName, Harness: piFamilyHarness(piModelsPath)}
	ran := false
	step := whenAPIKeyLogin(Step{Name: "test", Run: func(*Context) error { ran = true; return nil }})
	if err := step.Run(c); err != nil {
		t.Fatalf("API-key-only step: %v", err)
	}
	if ran {
		t.Fatal("the step ran without an API-key login")
	}
}

func TestAPIKeyLoginStepRequiresStoredLoginChecks(t *testing.T) {
	c := &Context{
		TB:    t,
		Agent: codexAgentName,
		Harness: Harness{Login: &LoginFacts{
			APIKey: func(string) string { return "codex login --with-api-key" },
		}},
	}
	err := whenAPIKeyLogin(Step{Name: "test", Run: func(*Context) error {
		t.Fatal("the step ran with incomplete facts")
		return nil
	}}).Run(c)
	if err == nil || !strings.Contains(err.Error(), "lacks stored-login checks") {
		t.Fatalf("API-key-only step error = %v, want missing status/sign-in facts", err)
	}
	if c.Session != nil {
		t.Fatal("the persistence step started a session with incomplete facts")
	}
}

func TestAPIKeyLoginStepsPreserveCheckFailuresAndContinueToQuit(t *testing.T) {
	var ran []string
	journey := Journey{Name: "persistence", Steps: []Step{
		whenAPIKeyLogin(Then(AsCheck(recordStep("stored status", &ran, errors.New("missing login"))))),
		whenAPIKeyLogin(When(recordStep("relaunch", &ran, nil))),
		whenAPIKeyLogin(Then(AsCheck(recordStep("signed in", &ran, errors.New("sign-in screen"))))),
		whenAPIKeyLogin(When(recordStep("quit", &ran, nil))),
	}}
	tb := &recordingTB{}
	runJourney(tb, &Context{Harness: codexHarness}, journey, fixedClock())
	if !slices.Equal(ran, []string{"stored status", "relaunch", "signed in", "quit"}) ||
		len(tb.errors) != 2 || len(tb.fatals) != 0 {
		t.Fatalf("ran %v, errors %v, fatals %v; want both failed checks and the final quit", ran, tb.errors, tb.fatals)
	}
}
