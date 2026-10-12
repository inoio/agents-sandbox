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
	written   int
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
	return r.written > 0
}

func (r *runner) Written() int {
	return r.written
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
	defaultValue := r.promptDefault(question)
	switch question.Kind {
	case ChoiceKind:
		return r.askChoice(question, defaultValue)
	case ValueKind:
		return r.askValue(question, defaultValue)
	case ConfirmKind:
		return r.askConfirm(question, defaultValue)
	case GroupConfirmKind:
		return r.askGroupConfirm(question, defaultValue)
	default:
		return "", false, fmt.Errorf("unsupported question kind %d", question.Kind)
	}
}

// promptDefault is the value offered when the user accepts a question without
// editing: the value already set in the user config when present, otherwise the
// catalog default. Accepting it therefore never changes an existing value.
func (r *runner) promptDefault(question SetupQuestion) string {
	if question.Kind == GroupConfirmKind || QuestionKey(question) == "" {
		return question.Default
	}
	value, ok := r.currentValue(question)
	if !ok {
		return question.Default
	}
	if question.Kind == ChoiceKind && !questionAllowsChoice(question, value) {
		return question.Default
	}
	return value
}

func (r *runner) currentValue(question SetupQuestion) (string, bool) {
	switch question.ValueType {
	case BoolValue:
		value, ok := r.config.GetBool(question.KeyPath)
		return strconv.FormatBool(value), ok
	case IntValue:
		value, ok := r.config.GetInt(question.KeyPath)
		return strconv.Itoa(value), ok
	case StringValue:
		return r.config.GetString(question.KeyPath)
	default:
		return "", false
	}
}

func questionAllowsChoice(question SetupQuestion, value string) bool {
	for _, choice := range question.Choices {
		if choice.Key == value {
			return true
		}
	}
	return false
}

func (r *runner) askChoice(question SetupQuestion, defaultValue string) (string, bool, error) {
	choices := r.withStopChoice(question, question.Choices)
	selected, err := r.ui.Select(question.Prompt, choices, defaultValue)
	if err != nil {
		return "", false, err
	}
	if selected == stopChoice {
		return "", true, nil
	}
	return selected, false, nil
}

func (r *runner) askValue(question SetupQuestion, defaultValue string) (string, bool, error) {
	choices := r.withStopChoice(question, []termio.Choice{
		{Key: defaultValue, Label: "Use default (" + defaultValue + ")", Description: ""},
		{Key: customChoice, Label: "Enter a custom value", Description: ""},
	})
	selected, err := r.ui.Select(question.Prompt, choices, defaultValue)
	if err != nil {
		return "", false, err
	}
	if selected == stopChoice {
		return "", true, nil
	}
	if selected != customChoice {
		return selected, false, nil
	}
	return r.askCustomValue(question, defaultValue)
}

func (r *runner) askCustomValue(question SetupQuestion, defaultValue string) (string, bool, error) {
	for {
		input, err := r.ui.Input(question.Prompt, defaultValue)
		if err != nil {
			return "", false, err
		}
		if input == "" {
			return defaultValue, false, nil
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

func (r *runner) askConfirm(question SetupQuestion, defaultValue string) (string, bool, error) {
	return r.askBool(question, defaultValue, yesNoChoices(confirmYes, confirmNo))
}

func (r *runner) askGroupConfirm(question SetupQuestion, defaultValue string) (string, bool, error) {
	return r.askBool(question, defaultValue, yesNoChoices(groupYes, groupNo))
}

func (r *runner) askBool(question SetupQuestion, defaultValue string, choices []termio.Choice) (string, bool, error) {
	selected, err := r.ui.Select(question.Prompt, r.withStopChoice(question, choices), defaultValue)
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
	return r.writeValue(question, value)
}

func (r *runner) applyDefault(question SetupQuestion) error {
	if r.shouldSkip(question) {
		return nil
	}
	if question.Kind == GroupConfirmKind {
		return r.applyGroupDefaults(question)
	}
	return r.writeValue(question, r.promptDefault(question))
}

func (r *runner) writeValue(question SetupQuestion, value string) error {
	if current, ok := r.currentValue(question); ok && current == value {
		r.state.MarkAsked(r.questionStateKey(question))
		return nil
	}
	if err := setQuestionValue(r.config, question, value); err != nil {
		return err
	}
	r.state.MarkAsked(r.questionStateKey(question))
	r.written++
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
	default:
		return fmt.Errorf("unsupported value type %d for %s", question.ValueType, QuestionKey(question))
	}
	return nil
}
