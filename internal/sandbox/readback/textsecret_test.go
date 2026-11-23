package readback_test

import (
	"testing"

	"github.com/inoio/agents-sandbox/internal/sandbox/readback"
)

func TestTextLeaksSecretRefusesALiteralSecretAssignment(t *testing.T) {
	leaking := map[string]string{
		"exported api key":          "Run it with:\n\n    export OPENAI_API_KEY=abc123def456ghi\n",
		"bare token":                "TOKEN=deadbeef1234\n",
		"yaml password":             "db:\n  password: hunter2hunter2\n",
		"inline code":               "Use `ANTHROPIC_API_KEY=sk_live_abcdefghij12` for tests.",
		"quoted js api key":         "const config = { apiKey = \"abcdefghijklmnopqrstu\" };",
		"quoted json key":           `{"client_secret": "abcd1234efgh"}`,
		"long secret without digit": "SECRET=abcdefghijklmnop\n",
		"vendor token in prose":     "my key is ghp_abcdefghijklmnopqrstuvwxyz0123",
		"session leaf marker":       "SESSION=abcd1234abcd\n",
		"second assignment":         "MODE=fast API_KEY=abcd1234efgh",
	}
	for name, text := range leaking {
		if !readback.TextLeaksSecret([]byte(text)) {
			t.Errorf("%s: TextLeaksSecret(%q) = false, want true", name, text)
		}
	}
}

func TestTextLeaksSecretAcceptsInstructionProse(t *testing.T) {
	plain := map[string]string{
		"placeholder reference": "export OPENAI_API_KEY=${OPENAI_API_KEY}\n",
		"shell variable":        "TOKEN=$GITHUB_TOKEN\n",
		"command substitution":  "TOKEN=$(gh auth token)\n",
		"angle placeholder":     "export OPENAI_API_KEY=<your-api-key>\n",
		"short value":           "TOKEN=abc12\n",
		"word below key length": "SECRET=abcdefghijklmno\n",
		"prose after a colon":   "Password: required for login.\n",
		"token count":           "max_tokens: 4096\n",
		"large token count":     "max_tokens: 128000000\n",
		"key file location":     "TOKEN_FILE=/run/secrets/token1\n",
		"non-secret name":       "MODEL=claude-sonnet-4-5-20250929\n",
		"heading":               "# Secrets\n\nNever commit credentials.\n",
		"empty":                 "",
		"ellipsis":              "export OPENAI_API_KEY=abc...\n",
	}
	for name, text := range plain {
		if readback.TextLeaksSecret([]byte(text)) {
			t.Errorf("%s: TextLeaksSecret(%q) = true, want false", name, text)
		}
	}
}

func TestTextLeaksSecretRefusesAStoredSecretNumber(t *testing.T) {
	if !readback.TextLeaksSecret([]byte("PIN=12345678\n")) {
		t.Error("a numeric value under a stored-secret name (PIN) must be refused")
	}
}
