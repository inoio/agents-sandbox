package main

import "gitlab.inoio.de/inoio/opencode-msb/internal/sandbox"

const (
	pFlagYes     = "yes"
	pFlagVerbose = "verbose"
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
	cmdImage   = "image"
	cmdVolume  = "volume"
	cmdSandbox = "sandbox"
	cmdPrune   = "prune"
	cmdStop    = "stop"
	cmdKill    = "kill"

	flagRebuild = "rebuild"
	flagCpus    = "cpus"
	flagMemory  = "memory"
	flagTmpSize = "tmp-size"

	annotationAlsoAs = sandbox.Prefix + "/also-as"
	annotationArgs   = sandbox.Prefix + "/args"
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

// NamedArg represents a named positional argument for display in usage and tree output.
type NamedArg struct {
	Name string `json:"name"`
	Help string `json:"help"`
}

const projectConfigDir = sandbox.ProjectDir
