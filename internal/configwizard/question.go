package configwizard

import (
	"strings"

	"github.com/inoio/agents-sandbox/internal/termio"
)

type Kind int

const (
	ChoiceKind Kind = iota
	ValueKind
	ConfirmKind
	GroupConfirmKind
)

type ValueType int

const (
	StringValue ValueType = iota
	BoolValue
	IntValue
)

type Tier = int

const (
	TierEssentials   Tier = 1
	TierResources    Tier = 2
	TierRuntime      Tier = 3
	TierIntegrations Tier = 4
)

type SetupQuestion struct {
	KeyPath          []string
	Tier             int
	Kind             Kind
	ValueType        ValueType
	Prompt           string
	Default          string
	Choices          []termio.Choice
	Validate         func(string) error
	Essential        bool
	AskOnlyIfUnset   bool
	AskEveryFirstRun bool
	Group            string
}

func QuestionKey(q SetupQuestion) string {
	return strings.Join(q.KeyPath, ".")
}
