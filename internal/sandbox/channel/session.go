package channel

import "github.com/inoio/agents-sandbox/internal/sandbox/network"

// Observer is a running observation channel: the overlay a run applies to its
// network options before the VM is prepared, and the teardown that returns the
// session's scrubbed artefact.
type Observer interface {
	// Overlay returns the policy the run must apply to its options.Network
	// before the VM is prepared, forcing interception on, routing the sandbox's
	// egress through this channel's proxy, and trusting the channel's CA
	// upstream.
	Overlay() network.Policy
	// Stop tears the proxy down and returns the session's scrubbed artefact. It
	// is idempotent: a second call returns the same artefact.
	Stop() Observation
}

// observer is the Observer a StartObservation call returns.
type observer struct {
	handle  Handle
	overlay network.Policy
}

// StartObservation binds the proxy and returns the Observer a run consults. The
// Observer's Overlay is the policy the run must apply to its options.Network
// BEFORE the VM is prepared — the overlay is creation-only; applying it later is
// a no-op. Stop tears the proxy down and returns the scrubbed artefact.
//
// The invocation is S7's: S7 starts the observer before prepareSandbox, assigns
// opts.Network = obs.Overlay() there, and calls Stop at teardown. This subplan
// does not touch session/options/prepare/vm.
//
//deadcode:ignore reason="the channel's facade entry point; S7 starts the observer before prepareSandbox"
func StartObservation(bindHost string, base network.Policy) (Observer, error) {
	handle, err := Start(bindHost)
	if err != nil {
		return nil, err
	}

	overlay := base.WithUpstreamCACerts(handle.CAFile())
	// The channel's proxy is on host loopback and unauthenticated, so it needs
	// no credentials.
	//nolint:exhaustruct_v5 // only the protocol and address describe the channel's own proxy
	overlay.Proxy = network.Proxy{Protocol: network.ProxyHTTPConnect, Address: handle.Addr}
	return observer{handle: handle, overlay: overlay}, nil
}

// Overlay returns the policy the run applies before preparing the VM.
//
//deadcode:ignore reason="reached from the observer StartObservation returns, whose invocation S7 wires"
func (o observer) Overlay() network.Policy { return o.overlay }

// Stop tears the proxy down and returns the scrubbed artefact.
//
//deadcode:ignore reason="reached from the observer StartObservation returns, whose invocation S7 wires"
func (o observer) Stop() Observation { return o.handle.Stop() }
