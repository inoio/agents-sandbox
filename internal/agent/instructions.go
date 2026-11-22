package agent

// InstructionSeeder declares an agent's credential-free instruction files: the
// global rules file and the prompt, command, subagent, and skill trees a user
// writes by hand. The first-run host seed copies them into the managed config
// dir, where the config-dir mirror carries them into the VM. It is an explicit
// allowlist, independent of the provision rules, so the seed never copies a
// whole config tree.
type InstructionSeeder interface {
	// InstructionPaths are paths relative to the directory of the agent's VM
	// config file (ConfigMerger.VMConfigPath), which is the mirror's root on
	// both the host and the VM. A trailing "/" marks a directory tree.
	InstructionPaths() []string
}

// AsInstructionSeeder returns the agent's InstructionSeeder, if it implements
// one.
func AsInstructionSeeder(a Agent) (InstructionSeeder, bool) {
	p, ok := a.(InstructionSeeder)
	return p, ok
}

// Instruction tree names several agents share, relative to their config dir.
const (
	agentsRulesFileName = "AGENTS.md"
	claudeRulesFileName = "CLAUDE.md"
	agentsDirName       = "agents/"
	commandsDirName     = "commands/"
	skillsDirName       = "skills/"
	promptsDirName      = "prompts/"
)
