package readback

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// sanitizeRefuses runs the guard on content and reports whether it refused.
func sanitizeRefuses(path, content string) bool {
	_, ok := SanitizeConfig([]byte(content), SanitizeParams{Path: path, RefuseUndeclaredSecretKeys: true})
	return !ok
}

// TestGuardRefusesASecretInAnUnanticipatedSpelling pins the fail-closed default:
// a credential under a key no rule names, inside a cleared string's pair, or in
// a {name,value} pair of any case is refused, because nothing proves it safe.
func TestGuardRefusesASecretInAnUnanticipatedSpelling(t *testing.T) {
	t.Parallel()
	for name, content := range map[string]string{
		"run-together accesstoken":             `{"accesstoken":"opaque-value-x"}`,
		"run-together clientsecret":            `{"clientsecret":"opaque-value-x"}`,
		"run-together secretkey":               `{"secretkey":"opaque-value-x"}`,
		"unknown key":                          `{"note":"opaque-value-x"}`,
		"assignment in a note":                 `{"note":"DB_PASSWORD=opaque-value-x"}`,
		"header line in a note":                `{"note":"x-api-key: \"opaque-value-x\""}`,
		"assignment in a command":              `{"command":"DB_PASSWORD=opaque-value-x mytool"}`,
		"exported assignment":                  `{"command":"export DB_PASSWORD=opaque-value-x && mytool"}`,
		"quoted -e assignment":                 `{"command":"docker run -e \"DB_PASSWORD=opaque-value-x\" img"}`,
		"quoted args assignment":               `{"args":["-e","\"DB_PASSWORD=opaque-value-x\""]}`,
		"unknown flag value":                   `{"args":["--frobnicate=opaque-value-x"]}`,
		"upper-case name/value":                `{"env":[{"NAME":"DB_PASSWORD","VALUE":"opaque-value-x"}]}`,
		"value under an unknown key in a list": `{"servers":[{"x":"opaque-value-x"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.True(t, sanitizeRefuses("settings.json", content), content)
		})
	}
}

// TestGuardAcceptsProvablySafeLeaves pins what the fail-closed default clears.
func TestGuardAcceptsProvablySafeLeaves(t *testing.T) {
	t.Parallel()
	for name, content := range map[string]string{
		"model and name":          `{"model":"gpt-5-codex","name":"corp"}`,
		"plain url":               `{"anything":"https://api.example.com/v1"}`,
		"path under a secret key": `{"env":{"GOOGLE_APPLICATION_CREDENTIALS":"/home/dev/gcp.json"}}`,
		"msb placeholder":         `{"apiKey":"$MSB_OPENAI_API_KEY"}`,
		"env-var name reference":  `{"env_key":"CORP_API_KEY"}`,
		"count as string":         `{"MAX_TOKENS":"8192"}`,
		"number and bool":         `{"retries":3,"enabled":true,"nothing":null}`,
		"safe assignment":         `{"command":"NODE_ENV=production LOG_LEVEL=debug mytool --port=8080"}`,
		"prose prompt":            `{"prompt":"Only implement basic functionality"}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.False(t, sanitizeRefuses("settings.json", content), content)
		})
	}
}

// TestUnprovenLeafPathNamesTheBlockingLeaf pins the reported path, so a refusal
// can say which key blocked it.
func TestUnprovenLeafPathNamesTheBlockingLeaf(t *testing.T) {
	t.Parallel()
	document := map[string]any{
		"model":   "x",
		"servers": []any{map[string]any{"name": "a"}, map[string]any{"note": "opaque"}},
	}

	require.Equal(t, "servers.[1].note", unprovenLeafPath(document))
	require.Empty(t, unprovenLeafPath(map[string]any{"model": "x"}))
}

func TestUnprovenLeafPathReportsTheFirstKeyInSortedOrder(t *testing.T) {
	t.Parallel()
	document := map[string]any{}
	for _, key := range []string{"k", "c", "j", "b", "i", "a", "h", "e", "g", "f", "d"} {
		document[key] = "opaque"
	}

	require.Equal(t, "a", unprovenLeafPath(document))
}

func TestUnprovenLeafPathWalksANonStringKeyedMapping(t *testing.T) {
	t.Parallel()

	require.Equal(t, "outer.note", unprovenLeafPath(map[string]any{"outer": map[any]any{"note": "opaque", 1: "x"}}))
}

func TestUnprovenLeafPathNamesAnUnprovenDocumentRoot(t *testing.T) {
	t.Parallel()

	require.Equal(t, documentRootPath, unprovenLeafPath("opaque"))
	require.Empty(t, unprovenLeafPath("https://api.example.com"))
}

func TestLeafIsProvablySafe(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		key  string
		leaf any
		safe bool
	}{
		"number under a count":          {"maxTokens", 4096, true},
		"number under a pin":            {"pin", 1234, false},
		"number under a session":        {"session", 1234, false},
		"number under an ordinary key":  {"retries", 3, true},
		"count as a string":             {"MAX_TOKENS", "8192", true},
		"number string under a pin":     {"pin", "1234", false},
		"number string under a session": {"session", "1234", false},
		"word under auth":               {"auth", "opaque", false},
		"env-var name under auth":       {"auth", "AUTH_TOKEN", true},
		"env-var name under a key":      {"apiKey", "OPENAI_API_KEY", true},
		"literal under a key":           {"apiKey", "opaque-value", false},
		"word under no key":             {"", "opaque", false},
		"word under a vocabulary key":   {"model", "opaque", true},
		"word under an unknown key":     {"note", "opaque", false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.safe, leafIsProvablySafe(tc.key, tc.leaf))
		})
	}
}

func TestIsPlainURL(t *testing.T) {
	t.Parallel()
	for value, plain := range map[string]bool{
		"https://api.example.com/v1":       true,
		"wss://stream.example.com":         true,
		"https://user:pw@api.example.com":  false,
		"https://api.example.com/v1?k=v":   false,
		"https://api.example.com/v1?":      false,
		"https://api.example.com/v1#frag":  false,
		"ftp://files.example.com/x":        false,
		"https:///no-host":                 false,
		"not a url":                        false,
		"https://bad host.example.com\x7f": false,
	} {
		require.Equal(t, plain, isPlainURL(value), value)
	}
}

func TestEmbeddedPairsAreProvablySafe(t *testing.T) {
	t.Parallel()
	for value, safe := range map[string]bool{
		"plain words only":                  true,
		"--flag without a value":            true,
		"=opaque has no name":               true,
		"a/b=opaque is no pair":             true,
		"mode=fast and LOG_LEVEL=debug":     true,
		"x=opaque":                          false,
		"--env=DB_PASSWORD=opaque":          false,
		"--env=LOG_LEVEL=debug":             true,
		"run \"API_KEY=opaque\"":            false,
		"set.dotted-thing=opaque is a pair": false,
	} {
		require.Equal(t, safe, embeddedPairsAreProvablySafe(value), value)
	}
}
