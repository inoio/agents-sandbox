package journeys

import "testing"

// TestEveryDrivenHarnessHasAnAdapter pins that a journey can only list a
// harness whose adapter tells its steps how to point it at the stub.
func TestEveryDrivenHarnessHasAnAdapter(t *testing.T) {
	for _, journey := range Journeys {
		for _, name := range journey.Harnesses {
			adapter, ok := Harnesses[name]
			if !ok {
				t.Errorf("journey %q drives %q, which has no adapter in Harnesses", journey.Name, name)
				continue
			}
			if adapter.StubConfig == nil {
				t.Errorf("the %s adapter has no StubConfig", name)
			}
		}
	}
}
