package configwizard

import (
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/inoio/agents-sandbox/internal/agent"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/viperconfig"
)

const (
	resourcesGroup = "resources"
	notifyGroup    = "notify"
	minUpgradeAge  = time.Hour
)

func DefaultCatalog() []SetupQuestion {
	questions := make([]SetupQuestion, 0, 16)
	questions = append(questions, essentialQuestions()...)
	questions = append(questions, resourceQuestions()...)
	questions = append(questions, runtimeQuestions()...)
	questions = append(questions, integrationQuestions()...)
	return questions
}

func essentialQuestions() []SetupQuestion {
	agentQuestion := choiceQuestion(
		[]string{"agent"},
		TierEssentials,
		"Which coding agent should run in the sandbox?",
		"opencode",
		agentChoices(),
	)
	agentQuestion.Essential = true
	agentQuestion.AskOnlyIfUnset = true

	networkQuestion := choiceQuestion(
		[]string{"network", "profile"},
		TierEssentials,
		"Which network profile should the sandbox use?",
		"none",
		stringChoices("none", "private", "host", "public"),
	)
	networkQuestion.Essential = true
	networkQuestion.AskEveryFirstRun = true

	return []SetupQuestion{agentQuestion, networkQuestion}
}

func resourceQuestions() []SetupQuestion {
	return []SetupQuestion{
		groupConfirmQuestion(
			TierResources,
			resourcesGroup,
			"Use the recommended resource defaults (4 CPUs, 8G memory, 16G disk, 16G quota)?",
			"yes",
		),
		valueQuestion(
			[]string{"cpus"},
			TierResources,
			IntValue,
			"How many CPUs should the sandbox use?",
			"4",
			resourcesGroup,
			validateCPUCount,
		),
		valueQuestion(
			[]string{"memory"},
			TierResources,
			StringValue,
			"Memory size for the sandbox (e.g. 8G)?",
			"8G",
			resourcesGroup,
			validateMemorySize,
		),
		valueQuestion(
			[]string{"disk-size"},
			TierResources,
			StringValue,
			"How large should the sandbox disk image be (e.g. 16G)?",
			"16G",
			resourcesGroup,
			validateMemorySize,
		),
		valueQuestion(
			[]string{"workspace-quota"},
			TierResources,
			StringValue,
			"Workspace quota for the sandbox (e.g. 16G)?",
			"16G",
			resourcesGroup,
			validateMemorySize,
		),
	}
}

func runtimeQuestions() []SetupQuestion {
	return []SetupQuestion{
		confirmQuestion([]string{"dind"}, TierRuntime, "Enable Docker-in-Docker in the sandbox?", "false", ""),
		valueQuestion(
			[]string{"tmp-size"},
			TierRuntime,
			StringValue,
			"Temporary filesystem size (e.g. 2G)?",
			"2G",
			"",
			validateMemorySize,
		),
		choiceQuestion(
			[]string{"log-level"},
			TierRuntime,
			"Which log level should the launcher use?",
			"info",
			stringChoices("error", "warning", "info", "verbose"),
		),
	}
}

func integrationQuestions() []SetupQuestion {
	audioQuestion := choiceQuestion(
		[]string{"notify", "audio"},
		TierIntegrations,
		"Notification sound?",
		"off",
		stringChoices("off", "system", "bell"),
	)
	audioQuestion.Group = notifyGroup

	return []SetupQuestion{
		groupConfirmQuestion(
			TierIntegrations,
			notifyGroup,
			"Use the recommended notification defaults?",
			"yes",
		),
		confirmQuestion(
			[]string{"notify", "desktop"},
			TierIntegrations,
			"Show desktop notifications?",
			"false",
			notifyGroup,
		),
		audioQuestion,
		choiceQuestion(
			[]string{"upgrade", "mode"},
			TierIntegrations,
			"How should agents-sandbox handle upgrades?",
			"prompt",
			stringChoices("prompt", "notify", "auto", "auto-exit"),
		),
		valueQuestion(
			[]string{"upgrade", "interval"},
			TierIntegrations,
			StringValue,
			"How often to check for upgrades (e.g. 1d)?",
			"1d",
			"",
			validateUpgradeInterval,
		),
		valueQuestion(
			[]string{"auto-prune-age"},
			TierIntegrations,
			StringValue,
			"Auto-prune sandboxes older than (e.g. 30d)?",
			"30d",
			"",
			validateAutoPruneAge,
		),
	}
}

func choiceQuestion(
	keyPath []string,
	tier int,
	prompt string,
	defaultValue string,
	choices []termio.Choice,
) SetupQuestion {
	return SetupQuestion{
		KeyPath:          keyPath,
		Tier:             tier,
		Kind:             ChoiceKind,
		ValueType:        StringValue,
		Prompt:           prompt,
		Default:          defaultValue,
		Choices:          choices,
		Validate:         nil,
		Essential:        false,
		AskOnlyIfUnset:   false,
		AskEveryFirstRun: false,
		Group:            "",
	}
}

func valueQuestion(
	keyPath []string,
	tier int,
	valueType ValueType,
	prompt string,
	defaultValue string,
	group string,
	validate func(string) error,
) SetupQuestion {
	return SetupQuestion{
		KeyPath:          keyPath,
		Tier:             tier,
		Kind:             ValueKind,
		ValueType:        valueType,
		Prompt:           prompt,
		Default:          defaultValue,
		Choices:          nil,
		Validate:         validate,
		Essential:        false,
		AskOnlyIfUnset:   false,
		AskEveryFirstRun: false,
		Group:            group,
	}
}

func confirmQuestion(keyPath []string, tier int, prompt, defaultValue, group string) SetupQuestion {
	return SetupQuestion{
		KeyPath:          keyPath,
		Tier:             tier,
		Kind:             ConfirmKind,
		ValueType:        BoolValue,
		Prompt:           prompt,
		Default:          defaultValue,
		Choices:          nil,
		Validate:         nil,
		Essential:        false,
		AskOnlyIfUnset:   false,
		AskEveryFirstRun: false,
		Group:            group,
	}
}

func groupConfirmQuestion(tier int, group, prompt, defaultValue string) SetupQuestion {
	return SetupQuestion{
		KeyPath:          nil,
		Tier:             tier,
		Kind:             GroupConfirmKind,
		ValueType:        StringValue,
		Prompt:           prompt,
		Default:          defaultValue,
		Choices:          nil,
		Validate:         nil,
		Essential:        false,
		AskOnlyIfUnset:   false,
		AskEveryFirstRun: false,
		Group:            group,
	}
}

func agentChoices() []termio.Choice {
	names := agent.Names()
	choices := make([]termio.Choice, 0, len(names))
	for _, name := range names {
		choices = append(choices, termio.Choice{Label: name, Key: name, Description: ""})
	}
	return choices
}

func stringChoices(keys ...string) []termio.Choice {
	choices := make([]termio.Choice, 0, len(keys))
	for _, key := range keys {
		choices = append(choices, termio.Choice{Label: key, Key: key, Description: ""})
	}
	return choices
}

func validateMemorySize(value string) error {
	if _, ok := options.ParseMemoryOK(value); !ok {
		return fmt.Errorf("invalid size %q", value)
	}
	return nil
}

func validateCPUCount(value string) error {
	count, err := strconv.Atoi(value)
	if err != nil {
		return fmt.Errorf("invalid CPU count %q", value)
	}
	if count < 0 || count > math.MaxUint8 {
		return fmt.Errorf("CPU count %q out of range 0-255", value)
	}
	return nil
}

func validateUpgradeInterval(value string) error {
	interval, ok := viperconfig.ParseHumanDuration(value)
	if !ok {
		return fmt.Errorf("invalid duration %q", value)
	}
	if interval < minUpgradeAge {
		return fmt.Errorf("upgrade interval %q must be at least 1h", value)
	}
	return nil
}

func validateAutoPruneAge(value string) error {
	age, ok := viperconfig.ParseHumanDuration(value)
	if !ok {
		return fmt.Errorf("invalid duration %q", value)
	}
	if age <= 0 {
		return fmt.Errorf("auto-prune age %q must be positive", value)
	}
	return nil
}
