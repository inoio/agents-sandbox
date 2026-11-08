package msb

// ViolationAction selects what happens when a secret placeholder is detected
// going to a host the secret isn't allowed to talk to. The string values match
// the runtime's so the launcher type can be translated verbatim.
type ViolationAction string

const (
	// ViolationActionDefault leaves the runtime default in place
	// (currently "block-and-log").
	ViolationActionDefault     ViolationAction = ""
	ViolationActionBlock       ViolationAction = "block"
	ViolationActionBlockAndLog ViolationAction = "block-and-log"
	// ViolationActionBlockAndTerminate also kills the sandbox.
	ViolationActionBlockAndTerminate ViolationAction = "block-and-terminate"
)

// SecretEntry is the launcher's projection of an SDK secret: a credential the
// network proxy substitutes at the transport layer. The value never enters the
// guest VM.
//
//exhaustruct:ignore
type SecretEntry struct {
	EnvVar             string
	Value              string
	Allow              []string
	Passthrough        []string
	Placeholder        string
	RequireTLSIdentity *bool
	Substitution       SecretSubstitution
	ViolationAction    ViolationAction
}

// SecretSubstitution selects request locations where substitution is enabled.
//
//exhaustruct:ignore
type SecretSubstitution struct {
	Headers      *bool
	HeaderFields []string
	Query        bool
	Body         bool
}

// SecretEnvOptions tunes Secret.Env beyond the required envVar and value.
//
//exhaustruct:ignore
type SecretEnvOptions struct {
	Allow []string
	// AllowPlaceholderFor lists hosts that may receive the unchanged placeholder
	// where substitution does not apply. Combined with Passthrough when both are set.
	AllowPlaceholderFor []string
	// Deprecated: use AllowPlaceholderFor.
	Passthrough        []string
	Placeholder        string
	RequireTLSIdentity *bool
	Substitution       SecretSubstitution
	ViolationAction    ViolationAction
}

// Secret is the factory namespace matching the SDK's Secret.Env.
var Secret secretFactory

type secretFactory struct{}

// Env mirrors microsandbox's Secret.Env, including folding Passthrough into
// AllowPlaceholderFor + Passthrough as the SDK does.
func (secretFactory) Env(envVar, value string, opts SecretEnvOptions) SecretEntry {
	placeholderHosts := append([]string(nil), opts.AllowPlaceholderFor...)
	placeholderHosts = append(placeholderHosts, opts.Passthrough...)
	return SecretEntry{
		EnvVar:             envVar,
		Value:              value,
		Allow:              opts.Allow,
		Passthrough:        placeholderHosts,
		Placeholder:        opts.Placeholder,
		RequireTLSIdentity: opts.RequireTLSIdentity,
		Substitution:       opts.Substitution,
		ViolationAction:    opts.ViolationAction,
	}
}
