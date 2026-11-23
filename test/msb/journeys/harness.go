package journeys

// Harness is what a journey's steps need to know about one harness beyond how
// to start and quit it (driver.Spec, which the startup smoke shares). A journey
// never names a harness's paths or formats itself; it asks the adapter.
//
//exhaustruct:ignore
type Harness struct {
	// StubConfig renders the VM-home config files (relative path to content)
	// that point the harness at the stub provider at addr. StepSeedStub
	// provisions them through the launcher's `home:` manifest.
	StubConfig func(addr string) map[string]string
	// HostConfig renders the harness's own host config files pointing at the
	// stub at addr, for a journey that seeds an existing host setup.
	HostConfig func(addr string) map[string]string
	// HostLoginStore is a host login store file (relative path to content) the
	// host seed must drop rather than copy.
	HostLoginStore map[string]string
}

// Harnesses maps an agent name to its adapter. Every harness a journey drives
// has one (TestEveryDrivenHarnessHasAnAdapter).
var Harnesses = map[string]Harness{
	opencode2AgentName: opencode2Harness,
}
