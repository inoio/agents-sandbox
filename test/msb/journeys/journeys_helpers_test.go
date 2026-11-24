package journeys

// fakeStubProvider is a StubProvider for a step test: StepSeedLearnSession only
// needs one present, and it reads the advertised host from the package's
// stubHost rather than the provider.
type fakeStubProvider struct{}

func (fakeStubProvider) Addr() string { return "http://stub.invalid" }

func (fakeStubProvider) Requests() []StubRequest { return nil }

func (fakeStubProvider) ScriptToolCall(ToolCall) {}

func (fakeStubProvider) Replies() []string { return nil }

func (fakeStubProvider) Stop() {}

// recordingStubProvider is a StubProvider whose served requests a test sets.
type recordingStubProvider struct {
	fakeStubProvider

	requests []StubRequest
}

func (r recordingStubProvider) Requests() []StubRequest { return r.requests }

// repliesStub is a StubProvider whose sent replies a test sets.
type repliesStub struct {
	fakeStubProvider

	replies []string
}

func (r repliesStub) Replies() []string { return r.replies }
