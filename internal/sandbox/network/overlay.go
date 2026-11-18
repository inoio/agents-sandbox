package network

import "slices"

// WithUpstreamCACerts returns a copy of the policy carrying the paths to the CA
// bundles the runtime must trust upstream, and forcing interception on.
//
// It is the learn-mode observation's in-memory overlay: the channel terminates
// TLS with its own CA, so the runtime must trust that CA to verify the
// upstreams it dials, and interception must be on for the channel to see the
// traffic at all. Forcing `on` means an overlay applied to an `off` or `auto`
// policy still intercepts.
//
// The paths are not policy content and never come from configuration: the field
// is unexported and untagged, so no key, env var, or flag expresses it. Describe
// deliberately omits it — it is not a user-facing setting — while Fingerprint
// includes it, so adding or changing the overlay recreates the VM.
func (p Policy) WithUpstreamCACerts(paths ...string) Policy {
	p.upstreamCACerts = slices.Clone(paths)
	p.InterceptTLS = InterceptOn
	return p
}

// UpstreamCACerts returns a copy of the upstream CA bundle paths carried by the
// observation overlay, or nil when it is not applied.
func (p Policy) UpstreamCACerts() []string {
	return slices.Clone(p.upstreamCACerts)
}
