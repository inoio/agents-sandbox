package readback

import "testing"

func TestLooksLikeSecretValue(t *testing.T) {
	secret := map[string]string{
		"x-api-key header":       "x-api-key: sk-ant-abcdef123456",
		"authorization header":   "Authorization=Bearer abcdefgh12345678",
		"bearer token":           "Bearer eyJhbGciOiJIUzI1NiJ9.payload",
		"basic auth":             "Basic dXNlcjpwYXNzd29yZA==",
		"proxy userinfo":         "http://alice:s3cret@proxy.corp:3128",
		"git url userinfo":       "https://oauth2:glpat-abcdefghij@gitlab.example/x.git",
		"anthropic key":          "sk-ant-api03-abcdefghijkl",
		"openai key in command":  "echo sk-proj-abcdefghijklmnop",
		"github pat":             "ghp_abcdefghijklmnopqrstuvwxyz0123",
		"github fine-grained":    "github_pat_11ABCDEFG0123456789_abcdef",
		"slack token":            "xoxb-123456789012-abcdefghijkl",
		"aws access key":         "AKIAIOSFODNN7EXAMPLE",
		"gitlab token":           "glpat-abcdefghij1234567890",
		"google api key":         "AIzaSyA-abcdefghijklmnopqrstuvwxyz12345",
		"hook with bearer token": `curl -H "Authorization: Bearer abcdefgh12345678" https://api.example`,
	}
	for name, value := range secret {
		if !looksLikeSecretValue(value) {
			t.Errorf("%s: looksLikeSecretValue(%q) = false, want true", name, value)
		}
	}

	plain := map[string]string{
		"model":            "claude-haiku-4-5",
		"url without auth": "https://api.anthropic.com/v1",
		"proxy no auth":    "http://proxy.corp:3128",
		"command":          "npx -y @modelcontextprotocol/server-filesystem /workspace",
		"word bearer":      "the bearer of news",
		"short sk":         "sk-1",
		"permission":       "Bash(ls:*)",
		"placeholder":      "${ANTHROPIC_API_KEY}",
	}
	for name, value := range plain {
		if looksLikeSecretValue(value) {
			t.Errorf("%s: looksLikeSecretValue(%q) = true, want false", name, value)
		}
	}
}

func TestEnvEntryIsSafe(t *testing.T) {
	safe := map[string]string{
		"ANTHROPIC_BASE_URL":            "https://gateway.example",
		"OTEL_EXPORTER_OTLP_ENDPOINT":   "https://otel.example:4318",
		"ANTHROPIC_MODEL":               "claude-opus-5-5",
		"CLAUDE_CODE_SUBAGENT_MODEL":    "claude-haiku-5-5",
		"AWS_REGION":                    "eu-central-1",
		"DISABLE_TELEMETRY":             "1",
		"CLAUDE_CODE_USE_BEDROCK":       "1",
		"MAX_THINKING_TOKENS":           "31999",
		"CLAUDE_CODE_MAX_OUTPUT_TOKENS": "8192",
		"BASH_DEFAULT_TIMEOUT_MS":       "60000",
		"ENABLE_FEATURE":                "true",
		"NO_PROXY":                      "localhost,127.0.0.1",
		"HTTPS_PROXY":                   "http://proxy.corp:3128",
		"LOG_LEVEL":                     "debug",
	}
	for key, value := range safe {
		if !envEntryIsSafe(key, value) {
			t.Errorf("envEntryIsSafe(%q, %q) = false, want true", key, value)
		}
	}

	unsafe := map[string]string{
		"ANTHROPIC_CUSTOM_HEADERS":   "x-api-key: abc",
		"OTEL_EXPORTER_OTLP_HEADERS": "Authorization=Bearer abc",
		"HTTPS_PROXY":                "http://alice:s3cret@proxy.corp:3128",
		"OPENAI_APIKEY":              "abcdef",
		"GH_PAT":                     "abcdef",
		"SOME_SETTING":               "opaque-value",
		"ANTHROPIC_MODEL":            "sk-ant-api03-abcdefghijkl",
	}
	for key, value := range unsafe {
		if envEntryIsSafe(key, value) {
			t.Errorf("envEntryIsSafe(%q, %q) = true, want false", key, value)
		}
	}
}

func TestIsNonSecretScalar(t *testing.T) {
	for _, value := range []string{"0", "31999", "-1", "1.5", "true", "FALSE", "yes", "off"} {
		if !isNonSecretScalar(value) {
			t.Errorf("isNonSecretScalar(%q) = false, want true", value)
		}
	}
	for _, value := range []string{"", "sk-1", "1e", "abc"} {
		if isNonSecretScalar(value) {
			t.Errorf("isNonSecretScalar(%q) = true, want false", value)
		}
	}
}
