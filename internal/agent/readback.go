package agent

// ReadBackDescriber declares what a harness's home read-back walks: the
// sandbox-home-relative config and credential paths whose change the baseline
// manifest tracks, and any agent-specific credential probes. It is optional,
// discovered by type assertion like the other capabilities; a profile without
// one yields no read-back facts rather than an error, so an unverified harness
// is honestly empty instead of a false "no credentials".
type ReadBackDescriber interface {
	// ReadBackPaths are sandbox-home-relative paths (files or globs) whose
	// change the baseline tracks and whose content the read-back inspects.
	ReadBackPaths() []string
	// CredentialProbes names agent-specific credential readers (e.g.
	// "opencode2-sqlite"); file-based auth is covered by ReadBackPaths.
	CredentialProbes() []string
}

// AsReadBackDescriber returns the agent's ReadBackDescriber, if it implements
// one.
//
//deadcode:ignore reason="read-back API; the pending learn-mode analyser (S6) is the production caller that type-asserts it"
func AsReadBackDescriber(a Agent) (ReadBackDescriber, bool) {
	p, ok := a.(ReadBackDescriber)
	return p, ok
}
