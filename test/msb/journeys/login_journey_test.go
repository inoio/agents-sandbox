package journeys

import "testing"

func TestMCPPersistenceWindowStartsAfterStopAndBeforeRelaunch(t *testing.T) {
	indexOf := func(name string) int {
		for index, step := range mcpLoginJourney.Steps {
			if step.Name == name {
				return index
			}
		}
		t.Fatalf("MCP journey lacks step %q", name)
		return -1
	}
	stop := indexOf(StepStopVM().Name)
	reset := indexOf(StepResetMCPCalls().Name)
	launch := indexOf(StepLaunch(runCommand).Name)
	restarted := indexOf(StepAssertVMRestarted().Name)
	assertToken := indexOf(StepAssertMCPCalledWithToken().Name)
	if stop >= reset || reset >= launch || launch >= restarted || restarted >= assertToken {
		t.Fatalf(
			"step order: stop=%d reset=%d launch=%d restarted=%d token=%d",
			stop,
			reset,
			launch,
			restarted,
			assertToken,
		)
	}
}
