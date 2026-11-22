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
		"openai key in command":  "echo sk-proj-abc123def456ghij",
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

func TestEnvEntryIsSafeByPrefix(t *testing.T) {
	if !envEntryIsSafe("DISABLE_FEATURES", "telemetry,autoupdate") {
		t.Error("a DISABLE_ switch with a word list was refused")
	}
}

func TestHeaderEntryIsSafe(t *testing.T) {
	cases := map[string]struct {
		name, value string
		want        bool
	}{
		"content type":           {"Content-Type", "application/json", true},
		"placeholder":            {"X-Custom-Auth", "${CUSTOM_AUTH}", true},
		"placeholdered bearer":   {"Authorization", "Bearer ${TOKEN}", true},
		"number, unknown header": {"X-Retry", "3", false},
		"number, known header":   {"Content-Length", "42", true},
		"unknown header":         {"X-Custom-Auth", "abcdef", false},
		"shaped value":           {"Accept", "Bearer abcdefgh12345678", false},
		"literal with a digit":   {"X-Custom", "id ${X} abc123def456", false},
	}
	for label, tc := range cases {
		if got := headerEntryIsSafe(tc.name, tc.value); got != tc.want {
			t.Errorf("%s: headerEntryIsSafe(%q, %q) = %v, want %v", label, tc.name, tc.value, got, tc.want)
		}
	}
}

func TestTableLeaksValueIgnoresOtherKeys(t *testing.T) {
	if tableLeaksValue("options", map[string]any{"X": "abcdef"}) {
		t.Error("a table that is neither env nor headers was judged by a table rule")
	}
}

func TestStringKeyedEntriesReadsAnyKeyedMaps(t *testing.T) {
	entries := stringKeyedEntries(map[any]any{"TOKEN": "abc", 1: "skipped"})
	if len(entries) != 1 || entries["TOKEN"] != "abc" {
		t.Errorf("entries = %v, want only the string key", entries)
	}
	if stringKeyedEntries("not a table") != nil {
		t.Error("a scalar yielded entries")
	}
}

func TestNameValuePairIsJudgedByTheEnvRule(t *testing.T) {
	if !decodedHoldsUndeclaredSecretValue(map[string]any{"name": "DB_PASSWORD", "value": "hunter2pass"}) {
		t.Error("a {name, value} env entry holding a password was accepted")
	}
	if decodedHoldsUndeclaredSecretValue(map[string]any{"name": "LOG_LEVEL", "value": "debug"}) {
		t.Error("a {name, value} env entry holding configuration was refused")
	}
}

func TestArgumentsLeakValue(t *testing.T) {
	cases := map[string]struct {
		arguments []any
		want      bool
	}{
		"flag then literal":          {[]any{"--access-token", "abcdef"}, true},
		"joined flag":                {[]any{"--client-secret=abcdef"}, true},
		"auth flag (leaf marker)":    {[]any{"--auth", "abcdef"}, true},
		"non-flag before the flag":   {[]any{"run", "--token", "abcdef"}, true},
		"other flag before the flag": {[]any{"--verbose", "--token", "abcdef"}, true},
		"secret flag last":           {[]any{"run", "--token"}, false},
		"location flag":              {[]any{"--token-file", "/run/secrets/t"}, false},
		"flag then another flag":     {[]any{"--token", "--verbose"}, false},
		"flag then placeholder":      {[]any{"--token", "${TOKEN}"}, false},
		"non-string before the flag": {[]any{1, "--token", "abcdef"}, true},
	}
	for name, tc := range cases {
		if got := argumentsLeakValue(tc.arguments); got != tc.want {
			t.Errorf("%s: argumentsLeakValue(%v) = %v, want %v", name, tc.arguments, got, tc.want)
		}
	}
}

func TestLiteralCredential(t *testing.T) {
	for value, want := range map[string]bool{"": false, "  ": false, "-x": false, "${X}": false, "{env:X}": false, "{file:/x}": false, "abc": true} {
		if got := literalCredential(value); got != want {
			t.Errorf("literalCredential(%q) = %v, want %v", value, got, want)
		}
	}
}

func TestIsPlaceholderReference(t *testing.T) {
	for value, want := range map[string]bool{"${X}": true, "{env:X}": true, "\t${X}": true, "{file:/x}": true, "X": false, "$X": false, "{other:X}": false} {
		if got := isPlaceholderReference(value); got != want {
			t.Errorf("isPlaceholderReference(%q) = %v, want %v", value, got, want)
		}
	}
}

func TestAssignmentAndNameValueLeaks(t *testing.T) {
	if !assignmentLeaksValue("API_KEY=abcdef") || assignmentLeaksValue("AWS_REGION=eu-central-1") ||
		!assignmentLeaksValue("LOG_LEVEL=sk-ant-api03-abc123def456") || assignmentLeaksValue(3) ||
		assignmentLeaksValue("not an assignment") {
		t.Error("assignmentLeaksValue misjudged an assignment")
	}
	if !nameValuePairLeaksValue(map[string]any{"name": "API_KEY", "value": "abcdef"}) ||
		nameValuePairLeaksValue(map[string]any{"value": "abcdef"}) ||
		nameValuePairLeaksValue(map[string]any{"name": "API_KEY"}) {
		t.Error("nameValuePairLeaksValue misjudged a pair")
	}
}

func TestEnvEntryIsSafeEdgeCases(t *testing.T) {
	cases := map[string]struct {
		key, value string
		want       bool
	}{
		"empty under a secret key":        {"API_KEY", "", true},
		"secret key with a safe suffix":   {"AUTH_TOKEN_MODE", "strict", false},
		"placeholder under a secret key":  {"API_KEY", "${API_KEY}", true},
		"16-character literal beside one": {"X_HEADERS", "a=${X},b=abcdefghijklmnop", false},
		"15-character literal beside one": {"X_HEADERS", "a=${X},b=abcdefghijklmno", true},
	}
	for name, tc := range cases {
		if got := envEntryIsSafe(tc.key, tc.value); got != tc.want {
			t.Errorf("%s: envEntryIsSafe(%q, %q) = %v, want %v", name, tc.key, tc.value, got, tc.want)
		}
	}
	if !headerEntryIsSafe("X-Auth", "") {
		t.Error("an empty header value was refused")
	}
}

func TestIsFilesystemPath(t *testing.T) {
	for value, want := range map[string]bool{
		"/home/dev/x": true, "~/a/b": true, "~/a": false, "/a": false, "./a/b": false, "a/b/c": false,
		"/x8Kd/93jf+zz==": false, "/home/dev/my file": false,
	} {
		if got := isFilesystemPath(value); got != want {
			t.Errorf("isFilesystemPath(%q) = %v, want %v", value, got, want)
		}
	}
}

func TestLooksLikeSecretValueChecksEveryMatch(t *testing.T) {
	if !looksLikeSecretValue("bearer authentication, then Bearer abc123def456") {
		t.Error("a digit-bearing token after a prose match was missed")
	}
}

func TestTableLeaksValueWalksNestedEntries(t *testing.T) {
	if !tableLeaksValue("env", map[string]any{"cfg": map[string]any{"note": "opaque"}}) {
		t.Error("a raw value in a table nested under env was accepted")
	}
	if tableLeaksValue("env", map[string]any{"cfg": map[string]any{"note": "${X}"}}) {
		t.Error("a reference in a table nested under env was refused")
	}
}
