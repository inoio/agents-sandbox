package configwizard

import (
	"fmt"
	"strconv"

	"github.com/inoio/agents-sandbox/internal/configfile"
	"github.com/inoio/agents-sandbox/internal/termio"
)

const (
	stopChoice      = "enough"
	customChoice    = "custom"
	stopChoiceLabel = "Enough questions — use defaults for the rest"
	groupYes        = "yes"
	groupNo         = "no"
	confirmYes      = "true"
	confirmNo       = "false"
)

type ScopeResolver interface {
	KeySetAboveUserConfig(key string) bool
}

type runner struct {
	questions []SetupQuestion
	tiers     map[int]bool
	firstRun  bool
	scope     ScopeResolver
	state     State
	ui        termio.UI
	config    *configfile.Config
	changed   bool
}

func (r *runner) run() (int, error) {
	for index, question := range r.questions {
		if !r.tiers[question.Tier] {
			continue
		}
		if r.shouldSkip(question) {
			continue
		}
		value, stop, err := r.ask(question)
		if err != nil {
			return index, err
		}
		if stop {
			if err := r.applyDefaultsFrom(index); err != nil {
				return index, err
			}
			return index, nil
		}
		if err := r.applyAnswer(question, value); err != nil {
			return index, err
		}
	}
	return len(r.questions), nil
}

func (r *runner) Changed() bool {
	return r.changed
}

func (r *runner) shouldSkip(question SetupQuestion) bool {
	stateKey := r.questionStateKey(question)
	if r.state.WasAsked(stateKey) && (!question.AskEveryFirstRun || !r.firstRun) {
		return true
	}
	if key := QuestionKey(question); key != "" && r.scope.KeySetAboveUserConfig(key) {
		r.ui.Warnf("%s is already set at a more specific scope; skipping", key)
		r.state.MarkAsked(stateKey)
		return true
	}
	if question.AskOnlyIfUnset {
		if _, present := r.config.GetString(question.KeyPath); present {
			return true
		}
	}
	return false
}

func (r *runner) questionStateKey(question SetupQuestion) string {
	if key := QuestionKey(question); key != "" {
		return key
	}
	return "group:" + question.Group
}

func (r *runner) ask(question SetupQuestion) (string, bool, error) {
	switch question.Kind {
	case ChoiceKind:
		return r.askChoice(question)
	case ValueKind:
		return r.askValue(question)
	case ConfirmKind:
		return r.askConfirm(question)
	case GroupConfirmKind:
		return r.askGroupConfirm(question)
	default:
		return "", false, fmt.Errorf("unsupported question kind %d", question.Kind)
	}
}

func (r *runner) askChoice(question SetupQuestion) (string, bool, error) {
	choices := r.withStopChoice(question, question.Choices)
	selected, err := r.ui.Select(question.Prompt, choices, question.Default)
	if err != nil {
		return "", false, err
	}
	if selected == stopChoice {
		return "", true, nil
	}
	return selected, false, nil
}

func (r *runner) askValue(question SetupQuestion) (string, bool, error) {
	choices := r.withStopChoice(question, []termio.Choice{
		{Key: question.Default, Label: "Use default (" + question.Default + ")", Description: ""},
		{Key: customChoice, Label: "Enter a custom value", Description: ""},
	})
	selected, err := r.ui.Select(question.Prompt, choices, question.Default)
	if err != nil {
		return "", false, err
	}
	if selected == stopChoice {
		return "", true, nil
	}
	if selected != customChoice {
		return selected, false, nil
	}
	return r.askCustomValue(question)
}

func (r *runner) askCustomValue(question SetupQuestion) (string, bool, error) {
	for {
		input, err := r.ui.Input(question.Prompt, question.Default)
		if err != nil {
			return "", false, err
		}
		if input == "" {
			return question.Default, false, nil
		}
		if question.Validate != nil {
			if err := question.Validate(input); err != nil {
				r.ui.Warn(err.Error())
				continue
			}
		}
		return input, false, nil
	}
}

func (r *runner) askConfirm(question SetupQuestion) (string, bool, error) {
	return r.askBool(question, yesNoChoices(confirmYes, confirmNo))
}

func (r *runner) askGroupConfirm(question SetupQuestion) (string, bool, error) {
	return r.askBool(question, yesNoChoices(groupYes, groupNo))
}

func (r *runner) askBool(question SetupQuestion, choices []termio.Choice) (string, bool, error) {
	selected, err := r.ui.Select(question.Prompt, r.withStopChoice(question, choices), question.Default)
	if err != nil {
		return "", false, err
	}
	if selected == stopChoice {
		return "", true, nil
	}
	return selected, false, nil
}

func (r *runner) withStopChoice(question SetupQuestion, choices []termio.Choice) []termio.Choice {
	result := make([]termio.Choice, 0, len(choices)+1)
	result = append(result, choices...)
	if !question.Essential {
		result = append(result, termio.Choice{Key: stopChoice, Label: stopChoiceLabel, Description: ""})
	}
	return result
}

func yesNoChoices(yesKey, noKey string) []termio.Choice {
	return []termio.Choice{
		{Key: yesKey, Label: "yes", Description: ""},
		{Key: noKey, Label: "no", Description: ""},
	}
}

func (r *runner) applyAnswer(question SetupQuestion, value string) error {
	if question.Kind == GroupConfirmKind {
		if value == groupYes {
			return r.applyGroupDefaults(question)
		}
		r.state.MarkAsked(r.questionStateKey(question))
		return nil
	}
	if err := setQuestionValue(r.config, question, value); err != nil {
		return err
	}
	r.state.MarkAsked(r.questionStateKey(question))
	r.changed = true
	return nil
}

func (r *runner) applyDefault(question SetupQuestion) error {
	if r.shouldSkip(question) {
		return nil
	}
	if question.Kind == GroupConfirmKind {
		return r.applyGroupDefaults(question)
	}
	if err := setQuestionValue(r.config, question, question.Default); err != nil {
		return err
	}
	r.state.MarkAsked(r.questionStateKey(question))
	r.changed = true
	return nil
}

func (r *runner) applyGroupDefaults(question SetupQuestion) error {
	for _, member := range r.groupMembers(question.Group) {
		if err := r.applyDefault(member); err != nil {
			return err
		}
	}
	r.state.MarkAsked(r.questionStateKey(question))
	return nil
}

func (r *runner) applyDefaultsFrom(start int) error {
	for _, question := range r.questions[start:] {
		if !r.tiers[question.Tier] {
			continue
		}
		if err := r.applyDefault(question); err != nil {
			return err
		}
	}
	return nil
}

func (r *runner) groupMembers(group string) []SetupQuestion {
	var members []SetupQuestion
	for _, question := range r.questions {
		if question.Group == group && question.Kind != GroupConfirmKind {
			members = append(members, question)
		}
	}
	return members
}

func setQuestionValue(cfg *configfile.Config, question SetupQuestion, value string) error {
	switch question.ValueType {
	case BoolValue:
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("parse boolean value %q for %s: %w", value, QuestionKey(question), err)
		}
		cfg.SetBool(question.KeyPath, parsed)
	case IntValue:
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("parse integer value %q for %s: %w", value, QuestionKey(question), err)
		}
		cfg.SetInt(question.KeyPath, parsed)
	case StringValue:
		cfg.SetString(question.KeyPath, value)
	}
	return nil
}
