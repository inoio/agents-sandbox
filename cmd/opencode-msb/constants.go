package main

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
	cmdImage   = "image"
	cmdVolume  = "volume"
	cmdStop    = "stop"
	cmdKill    = "kill"

	flagRebuild = "rebuild"
	flagCpus    = "cpus"
	flagMemory  = "memory"
	flagTmpSize = "tmp-size"

	annotationArgsDesc = "opencode-msb/args-description"
	annotationAlsoAs   = "opencode-msb/also-as"
	annotationArgs     = "opencode-msb/args"
)

// NamedArg represents a named positional argument for display in usage and tree output.
type NamedArg struct {
	Name string `json:"name"`
	Help string `json:"help"`
}

const projectLauncherDir = ".opencode-msb"
