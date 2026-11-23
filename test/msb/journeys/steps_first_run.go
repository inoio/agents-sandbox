package journeys

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v4"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/firstrun"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/test/msb/driver"
)

// StepChooseFirstRun answers the first-run menu with choice's key, which the
// launcher's termio Select reads as a line.
func StepChooseFirstRun(choice firstrun.Choice) Step {
	return Step{Name: "the user chooses " + string(choice) + " in the first-run menu", Run: func(c *Context) error {
		if _, err := session(c); err != nil {
			return err
		}
		return writeLine(c, string(choice))
	}}
}

// hostProvisioningQuestion is the start of the question the manual first-run
// choice asks before it provisions host config.
const hostProvisioningQuestion = "Provisioning copies your host agent config"

// StepDeclineHostProvisioning answers the manual first-run choice's
// host-config provisioning question with no, so the run provisions only what the
// journey seeded.
func StepDeclineHostProvisioning() Step {
	return Step{Name: "the user declines host-config provisioning", Run: func(c *Context) error {
		ses, err := session(c)
		if err != nil {
			return err
		}
		if err := ses.WaitForMarker(
			hostProvisioningQuestion,
			driver.ScaleTimeout(driver.TuiReadyTimeout, driver.SmokeTimeoutFactor()),
		); err != nil {
			return fmt.Errorf("the host-config provisioning question was not asked: %w", err)
		}
		return writeLine(c, "n")
	}}
}

// StepAssertFirstRunOffered blocks until the first-run menu prints choice's key,
// which both waits for the menu and asserts the choice was offered. A menu that
// omits the choice (no host config, so no "Learn from my host setup") never
// prints its key and the wait times out with the choice named.
func StepAssertFirstRunOffered(choice firstrun.Choice) Step {
	return Step{Name: "the first-run menu offers " + string(choice), Run: func(c *Context) error {
		ses, err := session(c)
		if err != nil {
			return err
		}
		if err := ses.WaitForMarker(
			string(choice),
			driver.ScaleTimeout(driver.TuiReadyTimeout, driver.SmokeTimeoutFactor()),
		); err != nil {
			return fmt.Errorf("first-run menu did not offer %q: %w", choice, err)
		}
		return nil
	}}
}

// StepAssertNoProjectScaffolding asserts the run created no `.agents-sandbox`
// directory in the project: a learning run writes the managed layer to the
// per-slug user scope, never the checkout (#252).
func StepAssertNoProjectScaffolding() Step {
	return AsCheck(Step{Name: "no project scaffolding appears", Run: func(c *Context) error {
		scaffold := filepath.Join(c.ProjectDir(), configpaths.ProjectConfigDir)
		_, err := os.Stat(scaffold)
		if err == nil {
			return fmt.Errorf("the run created project scaffolding at %s", scaffold)
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("stat %s: %w", scaffold, err)
		}
		return nil
	}})
}

// StepAssertFirstRunRecorded asserts the run recorded choice's first-run
// decision once in the isolated home's launcher-global RootState, under the
// "<repo slug>/<agent>" key the launcher uses. The menu's learning choices
// record state.FirstRunLearned, and the manual choice records
// state.FirstRunManual. It reads the root state file directly, not
// through state.ReadFirstRun: that resolves the process's real configpaths,
// while the run wrote under the journey's isolated XDG_STATE_HOME.
func StepAssertFirstRunRecorded(choice firstrun.Choice) Step {
	return AsCheck(Step{Name: "the first-run decision is recorded", Run: func(c *Context) error {
		want, ok := recordedFirstRunChoice(choice)
		if !ok {
			return fmt.Errorf("first-run choice %q records no RootState decision", choice)
		}
		identity, err := journeyIdentity(c)
		if err != nil {
			return err
		}
		path := firstRunStateFile(c)
		data, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("the run recorded no first-run decision: %s does not exist", path)
		}
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		var document struct {
			FirstRuns map[string]struct {
				Choice string `yaml:"choice"`
			} `yaml:"first_runs"`
		}
		if err := yaml.Unmarshal(data, &document); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		key := identity.RepoSlug + "/" + c.Agent
		record, ok := document.FirstRuns[key]
		if !ok {
			return fmt.Errorf(
				"the run recorded no first-run decision under %q in %s: %v\n%s",
				key,
				path,
				document.FirstRuns,
				data,
			)
		}
		if record.Choice != string(want) {
			return fmt.Errorf("first-run decision %q = %q, want %q", key, record.Choice, want)
		}
		return nil
	}})
}

// recordedFirstRunChoice maps a first-run menu choice to the choice the launcher
// records in RootState: both learning paths record FirstRunLearned, and the
// manual path records FirstRunManual (see firstrun.Outcome.Apply). It reports
// false for a choice no first-run path records.
func recordedFirstRunChoice(choice firstrun.Choice) (state.FirstRunChoice, bool) {
	switch choice {
	case firstrun.ChoiceLearnByUsing, firstrun.ChoiceLearnFromHost:
		return state.FirstRunLearned, true
	case firstrun.ChoiceManual:
		return state.FirstRunManual, true
	default:
		return "", false
	}
}

// firstRunStateFile is the isolated home's launcher-global state file, which
// holds the first-run records. It is not keyed by checkout: the records live at
// the state root (see state.RootState).
func firstRunStateFile(c *Context) string {
	return filepath.Join(c.Home, isolatedStateDir, launcherConfigDirName, "state.yaml")
}
