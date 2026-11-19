package agent

// EnvIndirectionDescriber declares how a harness reads a static credential from
// its config: a harness that does not expand a ${VAR} placeholder reads an
// environment-variable name from a config key instead, so the write-back emits
// that form. It is optional, discovered by type assertion like the other
// capabilities.
type EnvIndirectionDescriber interface {
	// EnvIndirectionKey is the config key the harness reads an environment
	// variable name from (kimi's "api_key_env"), or "" when the harness takes a
	// ${VAR} placeholder verbatim.
	EnvIndirectionKey() string
}

// AsEnvIndirectionDescriber returns the agent's EnvIndirectionDescriber, if it implements one.
//
//deadcode:ignore reason="write-back capability; the S10 after-run wiring supplies its caller"
func AsEnvIndirectionDescriber(a Agent) (EnvIndirectionDescriber, bool) {
	p, ok := a.(EnvIndirectionDescriber)
	return p, ok
}
