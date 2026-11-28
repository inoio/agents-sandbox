package journeys

import "testing"

func TestSecretSpikeHasSetupForEveryHarnessAndOptsInBeforeItsOwnProject(t *testing.T) {
	if secretSpikeJourney.Steps[0].Name != StepRequireOptIn(secretSpikeEnv, secretSpikeReason).Name {
		t.Fatal("the secret spike may create its own project before the opt-in check")
	}
	for _, name := range secretSpikeJourney.Harnesses {
		facts := Harnesses[name].Secret
		if facts == nil || facts.EnvVar == "" || facts.Header == "" ||
			(facts.Stub.HomeFiles == nil && facts.Stub.Env == nil && facts.Stub.Snippets == nil) {
			t.Errorf("the secret spike drives %s without complete secret setup", name)
		}
	}
}
