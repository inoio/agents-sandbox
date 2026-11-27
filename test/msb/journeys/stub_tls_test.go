package journeys

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A journey with a TLS stub provisions its CA through the home: manifest and
// points the harness at it through the env file; without one it does neither.
func TestStubCAReachesTheHomeAndTheEnvOnlyWithATLSStub(t *testing.T) {
	for _, useTLS := range []bool{true, false} {
		home := t.TempDir()
		c := &Context{TB: t, Agent: claudeCodeAgentName, Harness: claudeCodeHarness, Home: home}
		oauth, err := StartStubOAuthServer(t, useTLS)
		if err != nil {
			t.Fatal(err)
		}
		c.OAuth = oauth
		if err := StepSeedStub().Run(c); err != nil {
			t.Fatal(err)
		}
		if err := StepConfigureLauncherEnv().Run(c); err != nil {
			t.Fatal(err)
		}
		config, err := os.ReadFile(filepath.Join(home, "config", "agents-sandbox", "config.json"))
		if err != nil {
			t.Fatal(err)
		}
		env, err := os.ReadFile(filepath.Join(home, "config", "agents-sandbox", "env"))
		if err != nil {
			t.Fatal(err)
		}
		inManifest := strings.Contains(string(config), stubCAHomePath)
		inEnv := strings.Contains(string(env), stubCAEnv+"="+stubCAGuestPath)
		if inManifest != useTLS || inEnv != useTLS {
			t.Errorf("TLS %v: CA in the manifest %v, in the env %v; want both %v", useTLS, inManifest, inEnv, useTLS)
		}
	}
}

// A plain-HTTP stub has no CA and no roots to trust.
func TestPlainStubHasNoCA(t *testing.T) {
	stub, err := StartStubOAuthServer(t, false)
	if err != nil {
		t.Fatal(err)
	}
	if stub.CAPEM() != nil || stub.rootCAs() != nil {
		t.Error("a plain-HTTP stub reports a CA")
	}
}
