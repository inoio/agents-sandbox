package main

import (
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
	launcherconfig "github.com/inoio/agents-sandbox/internal/viperconfig"
)

const (
	pFlagYes      = "yes"
	pFlagLogLevel = "log-level"
	pFlagQuiet    = "quiet"
	pFlagNames    = "names"

	cmdRun        = "run"
	cmdShell      = "shell"
	cmdDoctor     = "doctor"
	cmdBuild      = "build"
	cmdDockerfile = "dockerfile"
	cmdList       = "list"
	cmdTree       = "tree"
	cmdVersion    = "version"
	cmdHelp       = "help"
	cmdCompletion = "completion"
	cmdConfig     = "config"
	cmdUpgrade    = "upgrade"
	cmdAgent      = "agent"
	cmdHome       = "home"
	cmdImage      = "image"
	cmdVolume     = "volume"
	cmdSandbox    = "sandbox"
	cmdPrune      = "prune"
	cmdStop       = "stop"
	cmdKill       = "kill"
	cmdMigrate    = "migrate"
	cmdReset      = "reset"
	cmdEdit       = "edit"

	flagRemove = "rm"

	flagRebuild         = launcherconfig.FlagRebuild
	flagCpus            = "cpus"
	flagMemory          = "memory"
	flagTmpSize         = "tmp-size"
	flagDiskSize        = "disk-size"
	flagWorkspaceQuota  = "workspace-quota"
	flagDryRun          = launcherconfig.FlagDryRun
	flagDryRunShort     = "n"
	flagDryRunVM        = launcherconfig.FlagDryRunVM
	flagForce           = "force"
	flagAge             = "age"
	flagWorktree        = launcherconfig.FlagWorktree
	flagRoot            = launcherconfig.FlagRoot
	flagServeOnly       = launcherconfig.FlagServeOnly
	flagNetwork         = launcherconfig.FlagNetwork
	flagDNSServers      = launcherconfig.FlagDNSServers
	flagAgent           = launcherconfig.FlagAgent
	flagNotify          = launcherconfig.FlagNotify
	flagDind            = "dind"
	flagAgentVersion    = "agent-version"
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
