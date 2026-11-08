package main

import (
	"github.com/spf13/cobra"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/git"
	"github.com/inoio/agents-sandbox/internal/projecttrust"
	"github.com/inoio/agents-sandbox/internal/runoptions"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// gateProjectConfig decides whether this command may use the checkout's
// project config layer and installs the decision into configpaths.Get, which
// every config reader derives its project inputs from. projectPaths is the
// process's path factory with the project layer; an earlier exclusion it may
// carry is undone first, so each command decides afresh.
//
// An approved (or empty) layer is used, and every later read of it must return
// exactly the bytes approved (projectguard). A changed layer is approved
// interactively by the commands that turn it into VM or image state, which
// refuse without a terminal; a dry run reports it, and every other command
// warns. A layer that ends up unapproved is left out of the run.
func gateProjectConfig(
	cmd *cobra.Command,
	ui termio.UI,
	identity git.Identity,
	projectPaths func() configpaths.ConfigPaths,
) error {
	withProject := func() configpaths.ConfigPaths { return configpaths.WithProjectLayer(projectPaths()) }
	configpaths.Use(withProject)

	decision, err := evaluateProjectConfig(withProject(), identity)
	if err != nil {
		return err
	}
	if decision.Approved {
		projecttrust.Guard(decision.Current, withProject().ProjectConfigDirAbs())
		return nil
	}
	switch {
	case !commandAppliesProjectConfig(cmd):
		ui.Warn(projecttrust.IgnoredWarning(decision))
	case runoptions.ResolveMode(cmd).Previews():
		ui.Warn(projecttrust.RenderChanges(decision.Changes) +
			"\nIt needs your approval before a run uses it; this preview leaves it out.")
	default:
		if err := projecttrust.Approve(ui, identity.CheckoutSlug, decision); err != nil {
			return err
		}
		projecttrust.Guard(decision.Current, withProject().ProjectConfigDirAbs())
		return nil
	}
	configpaths.Use(func() configpaths.ConfigPaths { return configpaths.WithoutProjectLayer(withProject()) })
	return nil
}

// evaluateProjectConfig computes the project layer's projection and compares it
// with the checkout's approval.
func evaluateProjectConfig(paths configpaths.ConfigPaths, identity git.Identity) (projecttrust.Decision, error) {
	projection, err := projecttrust.Compute(projecttrust.Sources{
		ProjectConfigDir:    paths.ProjectConfigDirAbs(),
		CheckoutRoot:        identity.BindRoot,
		AgentConfigDirNames: allAgentConfigDirNames(),
	})
	if err != nil {
		return projecttrust.Decision{}, err
	}
	// An unreadable approval reads as none, so the change is asked for again.
	approval, _, _ := projecttrust.LoadApproval(identity.CheckoutSlug)
	return projecttrust.Evaluate(projection, approval), nil
}

// allAgentConfigDirNames lists every config dir name a registered agent reads
// from the project config dir, primary names and aliases.
func allAgentConfigDirNames() []string {
	var names []string
	for _, name := range agent.Names() {
		profile, _ := agent.Lookup(name)
		// A registered profile's names are valid by construction; an invalid one
		// fails the run where the agent config is resolved.
		dirNames, _ := agent.ConfigDirNames(profile)
		names = append(names, dirNames...)
	}
	return names
}

// commandAppliesProjectConfig reports whether cmd turns the project config into
// VM or image state: the root command (a run), run, shell, and the image build.
func commandAppliesProjectConfig(cmd *cobra.Command) bool {
	if !cmd.HasParent() {
		return true
	}
	switch cmd.Name() {
	case cmdRun, cmdShell, cmdBuild:
		return true
	default:
		return false
	}
}
