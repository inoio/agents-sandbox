package main

import (
	"github.com/inoio/agents-sandbox/internal/runoptions"
	"github.com/inoio/agents-sandbox/internal/sandbox/naming"
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
	cmdPaths      = "paths"
	cmdImage      = "image"
	cmdVolume     = "volume"
	cmdSandbox    = "sandbox"
	cmdStorage    = "storage"
	cmdDF         = "df"
	cmdPrune      = "prune"
	cmdStop       = "stop"
	cmdKill       = "kill"
	cmdMigrate    = "migrate"
	cmdReset      = "reset"
	cmdEdit       = "edit"
	cmdRemove     = "remove"
	cmdInspect    = "inspect"
	cmdCompact    = "compact"
	cmdState      = "state"
	cmdRepair     = "repair"

	flagRemove = "rm"

	flagRebuild        = runoptions.FlagRebuild
	flagNoRebuild      = runoptions.FlagNoRebuild
	flagCpus           = "cpus"
	flagMemory         = "memory"
	flagTmpSize        = "tmp-size"
	flagDiskSize       = "disk-size"
	flagWorkspaceQuota = "workspace-quota"
	flagDryRun         = runoptions.FlagDryRun
	flagDryRunShort    = "n"
	flagDryRunVM       = runoptions.FlagDryRunVM
	flagForce          = "force"
	flagOlderThan      = "older-than"
	// flagAge is the deprecated alias for --older-than.
	flagAge        = "age"
	flagWorktree   = runoptions.FlagWorktree
	flagRoot       = runoptions.FlagRoot
	flagServeOnly  = runoptions.FlagServeOnly
	flagNetwork    = runoptions.FlagNetwork
	flagDNSServers = runoptions.FlagDNSServers
	flagAgent      = runoptions.FlagAgent
	flagNotify     = runoptions.FlagNotify
	flagOpenLinks  = runoptions.FlagOpenLinks
	flagDocker     = "docker"
	// flagDind is the deprecated alias for --docker.
	flagDind            = "dind"
	flagAgentVersion    = "agent-version"
	flagChannel         = "channel"
	flagOpenCodeVersion = "opencode-version"
	flagLabel           = "label"
	flagLimit           = "limit"
	flagRunning         = "running"
	flagStopped         = "stopped"
	flagFormat          = "format"
	flagVerbose         = "verbose"
	flagDisk            = "disk"
	flagRootOnly        = "root-only"
	flagLayers          = "layers"

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
	cmdRemoveAliases  = []string{"rm"}
)

// namedArg represents a named positional argument for display in usage and tree output.
type namedArg struct {
	Name string `json:"name"`
	Help string `json:"help"`
}
