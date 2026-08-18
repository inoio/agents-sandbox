package main

import "gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/naming"

const (
	pFlagYes     = "yes"
	pFlagVerbose = "verbose"
	pFlagError   = "error"
	pFlagQuiet   = "quiet"

	cmdRun     = "run"
	cmdShell   = "shell"
	cmdDoctor  = "doctor"
	cmdBuild   = "build"
	cmdList    = "list"
	cmdTree    = "tree"
	cmdVersion = "version"
	cmdConfig  = "config"
	cmdShow    = "show"
	cmdHome    = "home"
	cmdImage   = "image"
	cmdVolume  = "volume"
	cmdSandbox = "sandbox"
	cmdPrune   = "prune"
	cmdStop    = "stop"
	cmdKill    = "kill"
	cmdMigrate = "migrate"
	cmdReset   = "reset"
	cmdEdit    = "edit"

	flagRemove = "rm"

	flagRebuild         = "rebuild"
	flagCpus            = "cpus"
	flagMemory          = "memory"
	flagTmpSize         = "tmp-size"
	flagDiskSize        = "disk-size"
	flagWorkspaceQuota  = "workspace-quota"
	flagDryRun          = "dry-run"
	flagDryRunShort     = "n"
	flagDryRunVM        = "dry-run-vm"
	flagForce           = "force"
	flagAge             = "age"
	flagAll             = "all"
	flagWorktree        = "worktree"
	flagRoot            = "root"
	flagServeOnly       = "serve-only"
	flagOpenCodeVersion = "opencode-version"
	flagLabel           = "label"
	flagLimit           = "limit"
	flagRunning         = "running"
	flagStopped         = "stopped"
	flagFormat          = "format"
	formatJSON          = "json"

	annotationAlsoAs = naming.Prefix + "/also-as"
	annotationArgs   = naming.Prefix + "/args"
)

//nolint:gochecknoglobals // undeclarable as consts, but used as such
var (
	cmdListAliases    = []string{"ls"}
	cmdShellAliases   = []string{"sh"}
	cmdConfigAliases  = []string{"cfg"}
	cmdImageAliases   = []string{"img"}
	cmdVolumeAliases  = []string{"vol"}
	cmdSandboxAliases = []string{"sb"}
)

// namedArg represents a named positional argument for display in usage and tree output.
type namedArg struct {
	Name string `json:"name"`
	Help string `json:"help"`
}
