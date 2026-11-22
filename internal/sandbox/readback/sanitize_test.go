package readback

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRetainsCredentialValueUnknownFormat pins the fallback for a path whose
// extension configdecode does not know: only the literal check runs, so a
// value-free file is reported as retaining nothing.
func TestRetainsCredentialValueUnknownFormat(t *testing.T) {
	credentials := []Credential{{EnvVar: "OPENAI_API_KEY", Value: "sk-x"}}

	require.False(t, retainsCredentialValue(".config/opencode/notes.txt", "no secret here\n", credentials))
}

// TestRewriteNativeAssignmentsIgnoresAnEmptyValue pins the guard: with no value
// to rewrite, the content is returned unchanged.
func TestRewriteNativeAssignmentsIgnoresAnEmptyValue(t *testing.T) {
	const content = "api_key = \"\"\n"

	got := rewriteNativeAssignments(content, Credential{EnvVar: "MOONSHOT_API_KEY"}, "api_key_env")

	require.Equal(t, content, got)
}

// TestAssignmentKeyRangeRejectsASeparatorWithoutAKey pins the false return when a
// value follows an assignment separator with no key token before it: there is
// nothing to rewrite, so the caller's value-remains check refuses the file.
func TestAssignmentKeyRangeRejectsASeparatorWithoutAKey(t *testing.T) {
	const content = "+ = value"

	_, _, ok := assignmentKeyRange(content, strings.Index(content, "value"))

	require.False(t, ok)
}

// TestAssignmentKeyRange pins the key-token range the native rewrite replaces:
// the token before a '=' or ':' separator, with a quoted token's own quotes
// included (so replacing it leaves no dangling quote) and a dotted key's final
// segment alone, and no range when no key precedes the value.
func TestAssignmentKeyRange(t *testing.T) {
	tests := []struct {
		name    string
		content string
		value   string
		want    string
		ok      bool
	}{
		{name: "spaced equals", content: `api_key = "sk"`, value: "sk", want: "api_key", ok: true},
		{name: "tight equals", content: `api_key="sk"`, value: "sk", want: "api_key", ok: true},
		{name: "quoted key", content: `"api_key" = "sk"`, value: "sk", want: `"api_key"`, ok: true},
		{name: "dotted key", content: `providers.x.api_key = "sk"`, value: "sk", want: "api_key", ok: true},
		{name: "hyphenated key", content: `my-key = "sk"`, value: "sk", want: "my-key", ok: true},
		{name: "digits and underscore", content: `a1_2 = "sk"`, value: "sk", want: "a1_2", ok: true},
		{name: "colon separator", content: `key: "sk"`, value: "sk", want: "key", ok: true},
		{name: "tab around separator", content: "key:\t\"sk\"", value: "sk", want: "key", ok: true},
		{name: "unquoted value", content: `api_key=sk`, value: "sk", want: "api_key", ok: true},
		{name: "value whose prefix is whitespace", content: `  sk`, value: "sk", want: "", ok: false},
		{name: "colon with no key", content: `: "sk"`, value: "sk", want: "", ok: false},
		{name: "equals with no key", content: `= "sk"`, value: "sk", want: "", ok: false},
		{name: "non-assignment separator", content: `ab sk`, value: "sk", want: "", ok: false},
		{name: "value at content start", content: `"sk"`, value: "sk", want: "", ok: false},
		{name: "key with no closing quote", content: `"api=sk`, value: "sk", want: `"api`, ok: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			start, end, ok := assignmentKeyRange(test.content, strings.Index(test.content, test.value))
			require.Equal(t, test.ok, ok)
			if test.ok {
				require.Equal(t, test.want, test.content[start:end])
				return
			}
			require.Zero(t, start, "a refused range must report start 0")
			require.Zero(t, end, "a refused range must report end 0")
		})
	}
}

// TestSkipSpaceBackward pins the ASCII-whitespace walk: index moves left past
// every space/tab/CR/LF, and stops on any other byte or at the start.
func TestSkipSpaceBackward(t *testing.T) {
	require.Equal(t, 1, skipSpaceBackward("a   b", 4), "three spaces are skipped")
	require.Equal(t, 1, skipSpaceBackward("ab", 1), "a non-space stops the walk")
	require.Equal(t, 0, skipSpaceBackward("ab", 0), "index 0 stays")
	require.Equal(t, 0, skipSpaceBackward("   x", 3), "leading whitespace walks to the start")
}

// TestIsQuote pins the quote classifier to double and single quotes only.
func TestIsQuote(t *testing.T) {
	for _, b := range []byte{'"', '\''} {
		require.True(t, isQuote(b), "%q is a quote", b)
	}
	for _, b := range []byte{'a', '`', '='} {
		require.False(t, isQuote(b), "%q is not a quote", b)
	}
}

// TestEndsWithQuote pins the slice form of isQuote: only a trailing double or
// single quote matches, and an empty string matches neither.
func TestEndsWithQuote(t *testing.T) {
	require.True(t, endsWithQuote(`key"`))
	require.True(t, endsWithQuote(`key'`))
	require.False(t, endsWithQuote("key"))
	require.False(t, endsWithQuote(""), "an empty prefix ends with no quote")
}

// TestIsKeyChar pins the key-token classifier to ASCII letters, digits, '_' and
// '-'; a '.' is excluded so a dotted key's final segment is the token.
func TestIsKeyChar(t *testing.T) {
	for _, b := range []byte{'a', 'z', 'A', 'Z', '0', '9', '_', '-'} {
		require.True(t, isKeyChar(b), "%q is a key char", b)
	}
	for _, b := range []byte{'.', ' ', '=', '"', '/', 0xC3} {
		require.False(t, isKeyChar(b), "%q is not a key char", b)
	}
}

// TestIsSpaceByte pins the whitespace classifier to ASCII space, tab, CR and LF.
func TestIsSpaceByte(t *testing.T) {
	for _, b := range []byte{' ', '\t', '\r', '\n'} {
		require.True(t, isSpaceByte(b), "%q is whitespace", b)
	}
	for _, b := range []byte{'a', '=', '.', '\v'} {
		require.False(t, isSpaceByte(b), "%q is not whitespace", b)
	}
}

// TestUnquotedKey pins the key dequoting: a token with matching quotes loses
// them, and any other token — one quote only, two characters or fewer, or an
// unquoted name — is returned unchanged.
func TestUnquotedKey(t *testing.T) {
	require.Equal(t, "api_key", unquotedKey(`"api_key"`))
	require.Equal(t, "api_key", unquotedKey(`'api_key'`))
	require.Equal(t, "api_key", unquotedKey("api_key"))
	require.Equal(t, `"api`, unquotedKey(`"api`), "a leading quote with no closing quote is kept")
	require.Equal(t, `api"`, unquotedKey(`api"`), "a trailing quote with no opening quote is kept")
	require.Empty(t, unquotedKey(`""`))
	require.Equal(t, `"`, unquotedKey(`"`), "a one-byte token is not a quoted token")
}

// TestDecodedHoldsCredentialValueFindsAKey pins the decoded walk's key check
// directly: a value held as a document key is found, and one absent from every
// key and leaf is not.
func TestDecodedHoldsCredentialValueFindsAKey(t *testing.T) {
	credentials := []Credential{{Value: `sk-a"b`}}
	document := map[string]any{`sk-a"b`: "x"}

	require.True(t, decodedHoldsCredentialValue(document, credentials))
	require.False(t, decodedHoldsCredentialValue(map[string]any{"other": "x"}, credentials))
}

// TestDecodedHoldsCredentialValueWalksAnyMap pins the map[any]any walk directly:
// a string key holding the value is found, a non-string key is not a value, and
// a map without the value is not reported (so the fix does not over-refuse).
func TestDecodedHoldsCredentialValueWalksAnyMap(t *testing.T) {
	credentials := []Credential{{Value: "sk-x"}}

	require.True(t, decodedHoldsCredentialValue(map[any]any{"sk-x": "y"}, credentials))
	require.False(t, decodedHoldsCredentialValue(map[any]any{1: "y", "other": "z"}, credentials))
}

// TestSubstituteCredentialValuesRewritesEveryCredential pins that the
// substitution loop under a native indirection key processes every credential,
// not just the first: two credentials both yield their rewritten assignment and
// neither value survives.
func TestSubstituteCredentialValuesRewritesEveryCredential(t *testing.T) {
	const content = "one = \"sk-one\"\ntwo = \"sk-two\"\n"
	credentials := []Credential{
		{EnvVar: "ONE_KEY", Value: "sk-one"},
		{EnvVar: "TWO_KEY", Value: "sk-two"},
	}

	got, ok := substituteCredentialValues(
		content,
		credentials,
		"api_key_env",
		map[string]bool{"ONE_KEY": true, "TWO_KEY": true},
	)

	require.True(t, ok)
	require.NotContains(t, got, "sk-one")
	require.NotContains(t, got, "sk-two")
}

// TestLooksLikeSecretShapedKey pins the camelCase- and separator-aware marker
// match: every singular and plural marker segment matches, a key with no marker
// segment does not, and run-together apikey is matched as a whole.
func TestLooksLikeSecretShapedKey(t *testing.T) {
	secretShaped := []string{
		"api_key", "apiKey", "APIKEY", "accessToken", "access_token",
		"Authorization", "authorization", "clientSecret", "client_secret",
		"password", "PASSWORD", "credential", "privateKey", "bearerToken",
		"tokens", "refresh_tokens", "refreshTokens", "apiKeys", "API_KEYS",
		"secrets", "clientSecrets", "passwords", "credentials",
	}
	for _, key := range secretShaped {
		require.Truef(t, looksLikeSecretShapedKey(key), "%q is secret-shaped", key)
	}

	ordinary := []string{"model", "maxSize", "baseURL", "region", "timeout", "theme", "permission"}
	for _, key := range ordinary {
		require.Falsef(t, looksLikeSecretShapedKey(key), "%q is not secret-shaped", key)
	}
}

// TestSanitizeConfigSubstitutesAndAllows pins the shared happy path: a
// credential's value becomes its placeholder and the sanitized content is
// accepted.
func TestSanitizeConfigSubstitutesAndAllows(t *testing.T) {
	sanitized, ok := SanitizeConfig(
		[]byte("model:\n  api_key: sk-inline\n"),
		SanitizeParams{
			Path:        ".hermes/config.yaml",
			Credentials: []Credential{{EnvVar: "MOONSHOT_API_KEY", Value: "sk-inline", Hosts: []string{"h"}}},
		},
	)

	require.True(t, ok)
	require.Contains(t, sanitized, "${MOONSHOT_API_KEY}")
	require.NotContains(t, sanitized, "sk-inline")
}

// TestSanitizeConfigRefusesASurvivingValue pins the retention check: a value the
// substitution cannot reach keeps the file refused.
func TestSanitizeConfigRefusesASurvivingValue(t *testing.T) {
	_, ok := SanitizeConfig(
		[]byte("sk-survives\n"),
		SanitizeParams{
			Path:           ".hermes/config.yaml",
			Credentials:    []Credential{{EnvVar: "MOONSHOT_API_KEY", Value: "sk-other"}},
			AllCredentials: []Credential{{EnvVar: "MOONSHOT_API_KEY", Value: "sk-survives"}},
		},
	)

	require.False(t, ok)
}

// TestSanitizeConfigRefusesAnUndeclaredMCPAuthorization pins the host-seed guard:
// an MCP entry's Authorization header is not a classified credential, so its raw
// value would otherwise reach the managed config; the guard refuses the file.
func TestSanitizeConfigRefusesAnUndeclaredMCPAuthorization(t *testing.T) {
	content := []byte(`{"mcp":{"github":{"headers":{"Authorization":"Bearer sk-mcp-secret"}}}}`)

	_, ok := SanitizeConfig(content, SanitizeParams{
		Path:                       "opencode.jsonc",
		RefuseUndeclaredSecretKeys: true,
	})

	require.False(t, ok, "a raw Authorization header must refuse the file")
}

// TestSanitizeConfigRefusesAnUndeclaredAccessToken pins the guard for a provider
// entry key outside the recognized set: options.accessToken is not classified,
// so its raw value must refuse the file rather than be copied.
func TestSanitizeConfigRefusesAnUndeclaredAccessToken(t *testing.T) {
	content := []byte(`{"provider":{"openai":{"options":{"accessToken":"sk-raw-inline"}}}}`)

	_, ok := SanitizeConfig(content, SanitizeParams{
		Path:                       "opencode.jsonc",
		RefuseUndeclaredSecretKeys: true,
	})

	require.False(t, ok)
}

// TestSanitizeConfigRefusesAnUndeclaredSecretArray pins the guard for a
// secret-shaped key holding an array of raw strings: the walk must treat every
// descendant string as a leak, not only a direct string value.
func TestSanitizeConfigRefusesAnUndeclaredSecretArray(t *testing.T) {
	content := []byte(`{"apiKey":["sk-raw"]}`)

	_, ok := SanitizeConfig(content, SanitizeParams{
		Path:                       "opencode.jsonc",
		RefuseUndeclaredSecretKeys: true,
	})

	require.False(t, ok, "a raw secret in an array under a secret-shaped key must refuse the file")
}

// TestSanitizeConfigRefusesAnUndeclaredSecretArrayUnderPluralKey pins the guard
// for a run-together plural api-key name, which the segment walk alone does not
// recognize (apiKeys splits into API and KEYS).
func TestSanitizeConfigRefusesAnUndeclaredSecretArrayUnderPluralKey(t *testing.T) {
	content := []byte(`{"apiKeys":["sk-raw"]}`)

	_, ok := SanitizeConfig(content, SanitizeParams{
		Path:                       "opencode.jsonc",
		RefuseUndeclaredSecretKeys: true,
	})

	require.False(t, ok, "a raw secret under an apiKeys-style key must refuse the file")
}

// TestSanitizeConfigRefusesAnUndeclaredSecretObject pins the guard for a
// secret-shaped key holding a nested object of raw strings.
func TestSanitizeConfigRefusesAnUndeclaredSecretObject(t *testing.T) {
	content := []byte(`{"apiKey":{"default":"sk-raw"}}`)

	_, ok := SanitizeConfig(content, SanitizeParams{
		Path:                       "opencode.jsonc",
		RefuseUndeclaredSecretKeys: true,
	})

	require.False(t, ok, "a raw secret in an object under a secret-shaped key must refuse the file")
}

// TestSanitizeConfigGuardAcceptsAnOrphanFreeArray pins that the recursion does
// not over-refuse: an array of placeholders and references is not a leak.
func TestSanitizeConfigGuardAcceptsAnOrphanFreeArray(t *testing.T) {
	content := []byte(`{"apiKeys":["${OPENAI_API_KEY}","${ANTHROPIC_API_KEY}","OPENAI_API_KEY"]}`)

	_, ok := SanitizeConfig(content, SanitizeParams{
		Path:                       "opencode.jsonc",
		RefuseUndeclaredSecretKeys: true,
	})

	require.True(t, ok, "placeholders and references in an array are not leaks")
}

// TestSanitizeConfigGuardAcceptsAnOrphanFreeDocument pins that the guard does not
// over-refuse: a placeholder, a reference, an empty value, and a numeric token
// count are not leaks.
func TestSanitizeConfigGuardAcceptsAnOrphanFreeDocument(t *testing.T) {
	content := []byte(`{"apiKey":"${OPENAI_API_KEY}","token":"${TOKEN_VAR}",` +
		`"password":"","maxTokens":4096,"accessToken":"redacted"}`)

	_, ok := SanitizeConfig(content, SanitizeParams{
		Path:                       "opencode.jsonc",
		RefuseUndeclaredSecretKeys: true,
	})

	require.False(t, ok, "the literal accessToken value is still a leak")

	clean := []byte(`{"apiKey":"${OPENAI_API_KEY}","token":"${TOKEN_VAR}",` +
		`"password":"","maxTokens":4096}`)

	_, ok = SanitizeConfig(clean, SanitizeParams{
		Path:                       "opencode.jsonc",
		RefuseUndeclaredSecretKeys: true,
	})

	require.True(t, ok, "placeholders, an empty value, and a numeric count are not leaks")
}

// TestSanitizeConfigRefusesAnUndeclaredSecretUnderPluralFamilyKey pins the guard
// for the plural of each marker family: a plural secret-list key such as tokens,
// secrets, keys, credentials, or passwords must be secret-shaped, so a raw
// string inside it refuses the file.
func TestSanitizeConfigRefusesAnUndeclaredSecretUnderPluralFamilyKey(t *testing.T) {
	documents := []string{
		`{"tokens":["sk-raw"]}`,
		`{"credentials":{"a":"sk"}}`,
		`{"keys":["sk"]}`,
		`{"secrets":["sk-raw"]}`,
		`{"passwords":["sk-raw"]}`,
	}
	for _, document := range documents {
		_, ok := SanitizeConfig([]byte(document), SanitizeParams{
			Path:                       "opencode.jsonc",
			RefuseUndeclaredSecretKeys: true,
		})
		require.Falsef(t, ok, "%s must refuse the file", document)
	}
}

// TestSanitizeConfigGuardAcceptsAReferenceOnlyPluralKey pins that widening the
// key match does not over-refuse: a plural key whose values are references,
// numbers, or empty strings still holds no raw secret.
func TestSanitizeConfigGuardAcceptsAReferenceOnlyPluralKey(t *testing.T) {
	documents := []string{
		`{"tokens":["${T}"]}`,
		`{"maxTokens":4096}`,
		`{"passwords":[""]}`,
		`{"keys":[true,null]}`,
		`{"credentials":{"a":"${CRED_A}"}}`,
	}
	for _, document := range documents {
		_, ok := SanitizeConfig([]byte(document), SanitizeParams{
			Path:                       "opencode.jsonc",
			RefuseUndeclaredSecretKeys: true,
		})
		require.Truef(t, ok, "%s holds no raw secret", document)
	}
}

// TestSanitizeConfigGuardIsOffByDefault pins that the write-back path (which
// leaves RefuseUndeclaredSecretKeys false) is unchanged: an undeclared
// secret-shaped value does not refuse the file.
func TestSanitizeConfigGuardIsOffByDefault(t *testing.T) {
	content := []byte(`{"mcp":{"github":{"headers":{"Authorization":"Bearer sk-mcp-secret"}}}}`)

	_, ok := SanitizeConfig(content, SanitizeParams{Path: "opencode.jsonc"})

	require.True(t, ok)
}

// TestSanitizeConfigRefusesACredentialNotAllowed pins the substitution
// precondition: a credential whose env-var name the caller will not propose is
// refused, so SanitizeConfig reports ok false rather than emitting an orphan
// placeholder.
func TestSanitizeConfigRefusesACredentialNotAllowed(t *testing.T) {
	_, ok := SanitizeConfig([]byte("model:\n  api_key: sk-x\n"), SanitizeParams{
		Path:           ".hermes/config.yaml",
		Credentials:    []Credential{{EnvVar: "MOONSHOT_API_KEY", Value: "sk-x", Hosts: []string{"h"}}},
		AllowedEnvVars: map[string]bool{},
	})

	require.False(t, ok)
}

// TestHoldsLaterYAMLDocument pins the multi-document decision directly: the
// marker is a document start only when nothing but blank lines and comments
// precedes it, and lines are split on every break go-yaml honours, not LF alone.
func TestHoldsLaterYAMLDocument(t *testing.T) {
	for name, tc := range map[string]struct {
		content string
		want    bool
	}{
		"one document":                     {"model: x\n", false},
		"comment then a document start":    {"# agent config\n---\nmodel: x\n", false},
		"blank lines then a start":         {"\n\n---\nmodel: x\n", false},
		"a start alone":                    {"---\nmodel: x\n", false},
		"a document start without content": {"---\n", false},
		"no content":                       {"", false},
		"comment only":                     {"# agent config\n", false},
		"second document":                  {"model: x\n---\nnote: y\n", true},
		"second start only":                {"model: x\n---\n", true},
		"document after a comment":         {"model: x\n# note\n---\nnote: y\n", true},
		"document end marker":              {"model: x\n...\n", true},
		"second document start":            {"---\n---\nnote: y\n", true},
		"second document start after header": {
			"# header\n---\n---\nnote: y\n",
			true,
		},
		// A stream whose lines end in a non-LF break is split the way go-yaml
		// splits it, so its later document marker is seen.
		"second document, CR-only breaks":     {"model: x\r---\rnote: y\r", true},
		"second document, NEL breaks":         {"model: x\u0085---\u0085note: y\u0085", true},
		"second document, LS breaks":          {"model: x\u2028---\u2028note: y\u2028", true},
		"second document, PS breaks":          {"model: x\u2029---\u2029note: y\u2029", true},
		"document end marker, CR-only":        {"model: x\r...\r", true},
		"start then second document, CR-only": {"---\r---\rnote: y\r", true},
		"single document, CR-only breaks":     {"model: x\rnote: y\r", false},
	} {
		if got := holdsLaterYAMLDocument(tc.content); got != tc.want {
			t.Errorf("%s: holdsLaterYAMLDocument(%q) = %v, want %v", name, tc.content, got, tc.want)
		}
	}
}

// TestHoldsUndeclaredSecretValueUnknownFormat pins the unknown-format fallback:
// a path configdecode does not know cannot be decoded, so nothing is reported.
func TestHoldsUndeclaredSecretValueUnknownFormat(t *testing.T) {
	require.False(t, holdsUndeclaredSecretValue("notes.txt", "anything: raw\n"))
}

// TestHoldsUndeclaredSecretValueUndecodableIsARefusal pins that a known format
// that does not parse reports true, so a secret cannot hide behind an
// undecodable document.
func TestHoldsUndeclaredSecretValueUndecodableIsARefusal(t *testing.T) {
	require.True(t, holdsUndeclaredSecretValue("opencode.jsonc", "not json"))
}

// TestRetainsCredentialValueUndecodableIsARetention pins that a known format
// that does not parse is reported as retaining a value: the decoded walk cannot
// run, so an escaped form cannot be ruled out.
func TestRetainsCredentialValueUndecodableIsARetention(t *testing.T) {
	require.True(t, retainsCredentialValue(
		"opencode.jsonc",
		"not json",
		[]Credential{{Value: "sk-x"}},
	))
}

// TestDecodedHoldsCredentialValueWalksArrays pins the array branch of the
// decoded walk: a value held inside an array is found.
func TestDecodedHoldsCredentialValueWalksArrays(t *testing.T) {
	require.True(t, decodedHoldsCredentialValue(
		[]any{"other", "sk-x"},
		[]Credential{{Value: "sk-x"}},
	))
}

// TestAnyMapHoldsCredentialValueWalksNonStringKeyChildren pins that a mapping
// with a non-string key still has its descendant values walked.
func TestAnyMapHoldsCredentialValueWalksNonStringKeyChildren(t *testing.T) {
	require.True(t, anyMapHoldsCredentialValue(
		map[any]any{1: "sk-x"},
		[]Credential{{Value: "sk-x"}},
	))
}

// TestRewriteNativeAssignmentsLeavesAnUnkeyedOccurrence pins the no-key branch:
// an occurrence with no recognizable assignment key is left untouched, so the
// caller's value-remains check refuses the file rather than emitting the value.
func TestRewriteNativeAssignmentsLeavesAnUnkeyedOccurrence(t *testing.T) {
	const content = "+ sk-x\n"

	got := rewriteNativeAssignments(content, Credential{EnvVar: "MOONSHOT_API_KEY", Value: "sk-x"}, "api_key_env")

	require.Equal(t, content, got)
}

// TestRewriteNativeAssignmentsRewritesPastAValueAtTheStart pins the loop over
// every occurrence: a credential value at index 0 (no key before it, left
// untouched) must not stop the later, recognizable assignment from being
// rewritten. A boundary mutant that returns at the index-0 occurrence leaves
// the later assignment unrewritten.
func TestRewriteNativeAssignmentsRewritesPastAValueAtTheStart(t *testing.T) {
	const content = "sk-x\napi_key = \"sk-x\"\n"

	got := rewriteNativeAssignments(content, Credential{EnvVar: "MOONSHOT_API_KEY", Value: "sk-x"}, "api_key_env")

	require.Equal(t, "sk-x\napi_key_env = \"MOONSHOT_API_KEY\"\n", got)
}

// TestRewriteNativeAssignmentsKeepsAnEnvTableKey pins the env-table branch: an
// occurrence whose key is already the env-var name keeps its key and takes the
// placeholder instead of the native indirection key.
func TestRewriteNativeAssignmentsKeepsAnEnvTableKey(t *testing.T) {
	const content = "KIMI_API_KEY = \"sk-x\"\n"

	got := rewriteNativeAssignments(content, Credential{EnvVar: "KIMI_API_KEY", Value: "sk-x"}, "api_key_env")

	require.Contains(t, got, "${KIMI_API_KEY}")
	require.NotContains(t, got, "sk-x")
	require.NotContains(t, got, "api_key_env")
}

// TestDecodedHoldsUndeclaredSecretValueWalksAnyMap pins the map[any]any branch:
// a secret-shaped string key with a raw value is a leak, and a non-string key
// whose descendant holds one is a leak too.
func TestDecodedHoldsUndeclaredSecretValueWalksAnyMap(t *testing.T) {
	require.True(t, decodedHoldsUndeclaredSecretValue(map[any]any{"apiKey": "sk-raw"}))
	require.True(t, decodedHoldsUndeclaredSecretValue(map[any]any{
		1: map[string]any{"apiKey": "sk-raw"},
	}))
	require.False(t, decodedHoldsUndeclaredSecretValue(map[any]any{1: "ordinary"}))
}

// TestHoldsRawSecretStringWalksAnyMap pins the map[any]any branch of the raw
// secret walk: a non-string key's descendant string is still inspected.
func TestHoldsRawSecretStringWalksAnyMap(t *testing.T) {
	require.True(t, holdsRawSecretString(map[any]any{1: "sk-raw"}, false))
	require.False(t, holdsRawSecretString(map[any]any{1: map[string]any{"a": "${REF}"}}, false))
}

// TestHoldsUndeclaredSecretValueUnknownFormatIgnoresSecretShapedContent pins
// the unknown-format fallback: a path configdecode does not know cannot be
// decoded, so a secret-shaped key in its text is not reported (the caller
// decides the format policy).
func TestHoldsUndeclaredSecretValueUnknownFormatIgnoresSecretShapedContent(t *testing.T) {
	require.False(t, holdsUndeclaredSecretValue("notes.txt", `{"apiKey":"sk-raw"}`))
}

// TestSubstituteCredentialValuesRewritesTheNativeAssignment pins the exact
// native-indirection form: the assignment's key is replaced by the indirection
// key and its value by the env-var name, preserving the surrounding text (a
// dotted key's prefix included).
func TestSubstituteCredentialValuesRewritesTheNativeAssignment(t *testing.T) {
	got, ok := substituteCredentialValues(
		"model.api_key = \"sk-inline\"\n",
		[]Credential{{EnvVar: "MOONSHOT_API_KEY", Value: "sk-inline"}},
		"api_key_env",
		nil,
	)

	require.True(t, ok)
	require.Equal(t, "model.api_key_env = \"MOONSHOT_API_KEY\"\n", got)
}

// TestSubstituteCredentialValuesKeepsAnEnvTableKey pins the env-table branch:
// an assignment whose key is already the env-var name keeps that key and takes
// the placeholder instead of the indirection key.
func TestSubstituteCredentialValuesKeepsAnEnvTableKey(t *testing.T) {
	got, ok := substituteCredentialValues(
		"KIMI_API_KEY = \"sk-x\"\n",
		[]Credential{{EnvVar: "KIMI_API_KEY", Value: "sk-x"}},
		"api_key_env",
		nil,
	)

	require.True(t, ok)
	require.Equal(t, "KIMI_API_KEY = \"${KIMI_API_KEY}\"\n", got)
}

// TestSubstituteCredentialValuesReplacesTheLongestValueFirst pins the ordering:
// when one value is a prefix of another, the longer must be substituted first,
// or the shorter leaves a placeholder plus a stray suffix.
func TestSubstituteCredentialValuesReplacesTheLongestValueFirst(t *testing.T) {
	credentials := []Credential{
		{EnvVar: "SHORT_KEY", Value: "sk-abc"},
		{EnvVar: "LONG_KEY", Value: "sk-abcdef"},
	}

	got, ok := substituteCredentialValues("api_key = \"sk-abcdef\"\n", credentials, "", nil)

	require.True(t, ok)
	require.Equal(t, "api_key = \"${LONG_KEY}\"\n", got)
}

// TestSubstituteCredentialValuesRejectsAnEmptyEnvVar pins the substitution
// precondition: a credential with no env-var name has no placeholder to emit, so
// the whole content is refused rather than substituted with an empty name.
func TestSubstituteCredentialValuesRejectsAnEmptyEnvVar(t *testing.T) {
	_, ok := substituteCredentialValues(
		"api_key = \"sk-x\"\n",
		[]Credential{{Value: "sk-x"}},
		"",
		nil,
	)

	require.False(t, ok)
}

// TestRetainsCredentialValueFindsALiteralInAComment pins the literal half of the
// retention check: a value left in text the decoder discards (a comment) is
// still caught, because the literal content is searched as well.
func TestRetainsCredentialValueFindsALiteralInAComment(t *testing.T) {
	require.True(t, retainsCredentialValue(
		".hermes/config.yaml",
		"# sk-x\nmodel: y\n",
		[]Credential{{Value: "sk-x"}},
	))
}

// TestRetainsCredentialValueFindsAnEscapedDecodedValue pins the decoded half:
// a value stored in its escaped form (JSON's \") is missed by the literal search
// but found in the decoded document, so the file is still refused.
func TestRetainsCredentialValueFindsAnEscapedDecodedValue(t *testing.T) {
	require.True(t, retainsCredentialValue(
		"opencode.jsonc",
		`{"note":"sk-\"x"}`,
		[]Credential{{Value: `sk-"x`}},
	))
}

// TestDecodedHoldsCredentialValueWalksNestedChildren pins that a value nested
// below a non-credential key is found, not only a value at the top level.
func TestDecodedHoldsCredentialValueWalksNestedChildren(t *testing.T) {
	require.True(t, decodedHoldsCredentialValue(
		map[string]any{"outer": map[string]any{"inner": "sk-x"}},
		[]Credential{{Value: "sk-x"}},
	))
}

// TestContainsCredentialValueIgnoresAnEmptyValue pins the guard against the
// empty string: an empty value would otherwise match every text.
func TestContainsCredentialValueIgnoresAnEmptyValue(t *testing.T) {
	require.False(t, containsCredentialValue("anything", []Credential{{Value: ""}}))
}

// TestDecodedHoldsUndeclaredSecretValueWalksTopLevelArrays pins the []any branch
// of the undeclared-secret walk: a secret-shaped key inside a top-level array
// element is a leak.
func TestDecodedHoldsUndeclaredSecretValueWalksTopLevelArrays(t *testing.T) {
	require.True(t, decodedHoldsUndeclaredSecretValue(
		[]any{map[string]any{"apiKey": "sk-raw"}},
	))
}

// TestSecretKeyLeaksValueNeedsASecretShapedKey pins that the key test gates the
// value walk: an ordinary key holding a raw string is not a leak.
func TestSecretKeyLeaksValueNeedsASecretShapedKey(t *testing.T) {
	require.False(t, secretKeyLeaksValue("model", "sk-raw"))
}

// TestLooksLikeSecretShapedKeyRejectsANonMarkerPluralSegment pins that a
// non-marker segment ending in 'S' is not treated as a plural marker: status
// must not be secret-shaped.
func TestLooksLikeSecretShapedKeyRejectsANonMarkerPluralSegment(t *testing.T) {
	require.False(t, looksLikeSecretShapedKey("status"))
}

// TestKeySegments pins the word segmentation: separators and camelCase
// boundaries split segments, an empty segment between two separators is not
// emitted, and digits stay inside a segment.
func TestKeySegments(t *testing.T) {
	tests := []struct {
		key  string
		want []string
	}{
		{key: "a__b", want: []string{"A", "B"}},
		{key: "a_b", want: []string{"A", "B"}},
		{key: "a1b", want: []string{"A1B"}},
		{key: "accessToken", want: []string{"ACCESS", "TOKEN"}},
		{key: "API_KEY", want: []string{"API", "KEY"}},
		{key: "", want: nil},
	}
	for _, test := range tests {
		require.Equal(t, test.want, keySegments(test.key), "keySegments(%q)", test.key)
	}
}

// TestDecodedHoldsUndeclaredSecretValueAnyMapIgnoresOrdinaryKeys pins the
// map[any]any branch's key test: a string key that is not secret-shaped does not
// leak even when it holds a raw string.
func TestDecodedHoldsUndeclaredSecretValueAnyMapIgnoresOrdinaryKeys(t *testing.T) {
	require.False(t, decodedHoldsUndeclaredSecretValue(map[any]any{"model": "raw"}))
}

// TestSanitizeConfigRefusesUnmarkedSecretShapes pins the host-seed guard against
// credentials whose key names no secret: header values, URL userinfo, token
// prefixes in commands, compound key markers, and env-table entries, which are
// secret unless their key or value is known to be harmless.
func TestSanitizeConfigRefusesUnmarkedSecretShapes(t *testing.T) {
	refused := map[string]struct{ path, content string }{
		"custom headers":   {"settings.json", `{"env":{"ANTHROPIC_CUSTOM_HEADERS":"x-api-key: abc"}}`},
		"otel headers":     {"settings.json", `{"env":{"OTEL_EXPORTER_OTLP_HEADERS":"Authorization=Bearer abc"}}`},
		"opaque env value": {"settings.json", `{"env":{"SOME_SETTING":"opaque-value"}}`},
		"proxy userinfo":   {"settings.json", `{"env":{"HTTPS_PROXY":"http://alice:pw@proxy:3128"}}`},
		"hook bearer": {
			"settings.json",
			`{"hooks":{"PreToolUse":[{"command":"curl -H 'Authorization: Bearer abcdefgh12345678' x"}]}}`,
		},
		"helper command":      {"settings.json", `{"apiKeyHelper":"echo sk-proj-abc123def456ghij"}`},
		"compound key marker": {"settings.json", `{"OPENAI_APIKEY":"abcdef"}`},
		"nested mcp env (toml)": {
			"config.toml",
			"[mcp_servers.gh.env]\nGITHUB_PERSONAL_ACCESS_TOKEN = \"x\"\nGH_HOST_LABEL = \"opaque\"\n",
		},
		"url userinfo anywhere": {"config.toml", "[mcp_servers.git]\nurl = \"https://oauth2:abc@git.example/x\"\n"},
	}
	for name, tc := range refused {
		t.Run("refuses "+name, func(t *testing.T) {
			if _, ok := SanitizeConfig(
				[]byte(tc.content),
				SanitizeParams{Path: tc.path, RefuseUndeclaredSecretKeys: true},
			); ok {
				t.Errorf("SanitizeConfig accepted %s", tc.content)
			}
		})
	}

	accepted := map[string]struct{ path, content string }{
		"harmless claude settings": {"settings.json", `{"model":"haiku","permissions":{"allow":["Bash(ls:*)"]},` +
			`"env":{"MAX_THINKING_TOKENS":"31999","CLAUDE_CODE_MAX_OUTPUT_TOKENS":"8192","ANTHROPIC_BASE_URL":"https://gw.example",` +
			`"DISABLE_TELEMETRY":"1"}}`},
		"placeholder in env": {"settings.json", `{"env":{"ANTHROPIC_API_KEY":"${ANTHROPIC_API_KEY}"}}`},
		"plain codex settings": {
			"config.toml",
			"model = \"gpt-5\"\nsandbox_mode = \"workspace-write\"\n[mcp_servers.fs]\ncommand = \"npx\"\nargs = [\"-y\", \"server-filesystem\"]\n",
		},
	}
	for name, tc := range accepted {
		t.Run("accepts "+name, func(t *testing.T) {
			if _, ok := SanitizeConfig(
				[]byte(tc.content),
				SanitizeParams{Path: tc.path, RefuseUndeclaredSecretKeys: true},
			); !ok {
				t.Errorf("SanitizeConfig refused %s", tc.content)
			}
		})
	}
}

// TestSanitizeConfigReviewCases pins the guard's adversarial-review cases: the
// bypasses it must refuse and the legitimate configs it must still accept.
func TestSanitizeConfigReviewCases(t *testing.T) {
	refused := map[string]struct{ path, content string }{
		"query api key": {
			"opencode.json",
			`{"mcp":{"s":{"type":"remote","url":"https://server.example/mcp?api_key=0123abcd-4567&profile=p"}}}`,
		},
		"query token in env url": {"settings.json", `{"env":{"MCP_URL":"https://m.example/sse?token=abc123def456"}}`},
		"jsonc comment": {
			"opencode.jsonc",
			"{\n // \"apiKey\": \"sk-ant-api03-abcdefghijkl\",\n \"model\":\"x\"}",
		},
		"toml comment":         {"config.toml", "# OPENAI_API_KEY = \"sk-proj-abc123def456ghij\"\nmodel = \"x\"\n"},
		"second yaml document": {"config.yaml", "model: x\n---\napi_key: plainvalue\n"},
		"custom header":        {"opencode.json", `{"mcp":{"x":{"headers":{"X-Custom-Auth":"abc123def456"}}}}`},
		"toml http header": {
			"config.toml",
			"[mcp_servers.x]\nhttp_headers = { \"X-Auth\" = \"abc123def456\" }\n",
		},
		"cookie header":         {"opencode.json", `{"headers":{"Cookie":"session=abcdef123456"}}`},
		"auth key":              {"settings.json", `{"auth":"opaque-token-value"}`},
		"numeric password":      {"opencode.json", `{"mcp":{"x":{"password":"84736251"}}}`},
		"numeric env password":  {"opencode.json", `{"mcp":{"x":{"environment":{"DB_PASSWORD":"84736251"}}}}`},
		"upper-case env secret": {"settings.json", `{"env":{"TOTP_SECRET":"JBSWY3DPEHPK3PXP"}}`},
		"bang env value":        {"settings.json", `{"env":{"password":"!Sup3rS3cret"}}`},
		"redis empty user":      {"config.toml", "url = \"redis://:hunter2pass@redis:6379\"\n"},
		"password with slash":   {"settings.json", `{"env":{"DATABASE_URL":"postgres://app:ab/cd@db/x"}}`},
		"token as username":     {"config.toml", "url = \"https://abc123def456ghi@git.example/x\"\n"},
		"token flag":            {"config.toml", "[mcp_servers.x]\nargs = [\"--token\", \"abc123def456\"]\n"},
		"password flag":         {"config.toml", "[mcp_servers.x]\nargs = [\"--password=hunter2pass\"]\n"},
		"dsn password":          {"config.toml", "dsn = \"host=db user=app password=hunter2pass\"\n"},
		"passwd key":            {"config.toml", "passwd = \"hunter2pass\"\n"},
		"xai key":               {"config.toml", "note = \"xai-abcdefghijklmnopqrstuvwxyz\"\n"},
		"stripe key":            {"config.toml", "note = \"sk_live_abcdefghijklmnop\"\n"},
		"jwt":                   {"config.toml", "note = \"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.abcdefghij\"\n"},
		"pem key": {
			"config.toml",
			"pem = \"-----BEGIN PRIVATE KEY-----\\nMIIEv\\n-----END PRIVATE KEY-----\"\n",
		},
	}
	for name, tc := range refused {
		t.Run("refuses "+name, func(t *testing.T) {
			if _, ok := SanitizeConfig(
				[]byte(tc.content),
				SanitizeParams{Path: tc.path, RefuseUndeclaredSecretKeys: true},
			); ok {
				t.Errorf("SanitizeConfig accepted %s", tc.content)
			}
		})
	}

	accepted := map[string]struct{ path, content string }{
		"placeholdered header": {
			"settings.json",
			`{"env":{"OTEL_EXPORTER_OTLP_HEADERS":"Authorization=Bearer ${OTEL_TOKEN}"}}`,
		},
		"placeholdered api key": {"settings.json", `{"env":{"ANTHROPIC_CUSTOM_HEADERS":"x-api-key: ${X_API_KEY}"}}`},
		"prose basic":           {"opencode.json", `{"agent":{"x":{"prompt":"Only implement basic functionality"}}}`},
		"prose bearer": {
			"opencode.json",
			`{"agent":{"x":{"prompt":"The API uses bearer authentication and an api-key: header"}}}`,
		},
		"permission with $TOKEN": {
			"settings.json",
			`{"permissions":{"allow":["Bash(curl -H 'Authorization: Bearer $TOKEN':*)"]}}`,
		},
		"ordinary env entries": {
			"opencode.json",
			`{"mcp":{"py":{"environment":{"PYTHONPATH":"/opt/lib","PATH":"/usr/bin",` +
				`"HOME":"/home/dev","NODE_OPTIONS":"--max-old-space-size=4096","AWS_PROFILE":"dev","ANTHROPIC_VERTEX_PROJECT_ID":"proj-1",` +
				`"OTEL_EXPORTER_OTLP_PROTOCOL":"http/protobuf","OTEL_METRICS_EXPORTER":"otlp","GITHUB_TOOLSETS":"repos,issues",` +
				`"GIT_AUTHOR_NAME":"Dev","SSH_KEY_PATH":"/home/dev/.ssh/id","GOOGLE_APPLICATION_CREDENTIALS":"/home/dev/gcp.json"}}}}`,
		},
		"opencode env reference": {
			"opencode.json",
			`{"provider":{"anthropic":{"options":{"apiKey":"{env:ANTHROPIC_API_KEY}"}}}}`,
		},
		"opencode file ref": {
			"opencode.json",
			`{"provider":{"x":{"options":{"apiKey":"{file:~/.secrets/key}"}}}}`,
		},
		"max tokens":        {"settings.json", `{"env":{"MAX_THINKING_TOKENS":"31999"},"maxTokens":4096}`},
		"url without query": {"opencode.json", `{"mcp":{"s":{"url":"https://server.example/mcp"}}}`},
		"content type header": {
			"opencode.json",
			`{"mcp":{"x":{"headers":{"Content-Type":"application/json","anthropic-beta":"x-2025"}}}}`,
		},
		"risk word": {"opencode.json", `{"agent":{"x":{"prompt":"run the risk-assessment-tool"}}}`},
	}
	for name, tc := range accepted {
		t.Run("accepts "+name, func(t *testing.T) {
			if _, ok := SanitizeConfig(
				[]byte(tc.content),
				SanitizeParams{Path: tc.path, RefuseUndeclaredSecretKeys: true},
			); !ok {
				t.Errorf("SanitizeConfig refused %s", tc.content)
			}
		})
	}
}

// TestSanitizeConfigSecondReviewCases pins the second review round's bypasses
// and false positives.
func TestSanitizeConfigSecondReviewCases(t *testing.T) {
	refused := map[string]struct{ path, content string }{
		"numeric password json":   {"opencode.json", `{"mcp":{"x":{"password":84736251}}}`},
		"numeric password yaml":   {"config.yaml", "db:\n  password: 84736251\n"},
		"numeric api key":         {"opencode.json", `{"x":{"apiKey":12345678901234}}`},
		"pass key":                {"opencode.json", `{"x":{"pass":"hunter2pass"}}`},
		"db_pass key":             {"opencode.json", `{"x":{"db_pass":"hunter2pass"}}`},
		"pin key":                 {"opencode.json", `{"x":{"pin":"1234"}}`},
		"nested table in env":     {"opencode.json", `{"env":{"cfg":{"password":"hunter2pass"}}}`},
		"nested table in env 2":   {"opencode.json", `{"environment":{"prod":{"apiKey":"abc123def456"}}}`},
		"header list value":       {"opencode.json", `{"headers":{"X-Auth":["abc123def456"]}}`},
		"env array":               {"opencode.json", `{"x":{"env":["DB_PASSWORD=hunter2pass"]}}`},
		"env array yaml":          {"config.yaml", "env:\n  - GITHUB_PAT=abc123def456\n"},
		"numeric name-value pair": {"opencode.json", `{"env":[{"name":"DB_PASSWORD","value":84736251}]}`},
		"numeric name-value pair yaml": {
			"config.yaml",
			"env:\n  - name: DB_PASSWORD\n    value: 84736251\n",
		},
		"docker -e arg": {
			"config.toml",
			"[mcp_servers.x]\nargs = [\"run\", \"-e\", \"API_KEY=abc123def456\"]\n",
		},
		"access token flag":        {"config.toml", "args = [\"--access-token\", \"abcdef\"]\n"},
		"access token flag joined": {"config.toml", "args = [\"--access-token=abcdef\"]\n"},
		"client secret flag":       {"config.toml", "args = [\"--client-secret\", \"abcdef\"]\n"},
		"numeric access token flag": {
			"config.toml",
			"[mcp_servers.x]\nargs = [\"--access-token\", 84736251]\n",
		},
		"numeric password flag": {
			"config.toml",
			"[mcp_servers.x]\nargs = [\"--password\", 1234]\n",
		},
		"numeric access token flag yaml": {
			"config.yaml",
			"args:\n  - --access-token\n  - 84736251\n",
		},
		"path-shaped secret": {"settings.json", `{"env":{"API_SECRET":"/x8Kd93jfLq0aB+zz=="}}`},
		"literal beside placeholder": {
			"settings.json",
			`{"env":{"OTEL_EXPORTER_OTLP_HEADERS":"Authorization=Bearer ${X},X-Other=abcdefghijklmnopqrst"}}`,
		},
		"numeric header": {"opencode.json", `{"headers":{"X-Api-Key":"12345678901234"}}`},
	}
	for name, tc := range refused {
		t.Run("refuses "+name, func(t *testing.T) {
			if _, ok := SanitizeConfig(
				[]byte(tc.content),
				SanitizeParams{Path: tc.path, RefuseUndeclaredSecretKeys: true},
			); ok {
				t.Errorf("SanitizeConfig accepted %s", tc.content)
			}
		})
	}

	accepted := map[string]struct{ path, content string }{
		"placeholder url password":   {"settings.json", `{"env":{"DATABASE_URL":"postgres://app:${DB_PW}@db/x"}}`},
		"placeholder url password 2": {"opencode.json", `{"x":{"url":"https://u:${X}@h.example/p"}}`},
		"permission with url token": {
			"settings.json",
			`{"permissions":{"allow":["Bash(git push https://x:${TOKEN}@github.com:*)"]}}`,
		},
		"max tokens number":      {"opencode.json", `{"x":{"maxTokens":4096,"max_tokens":8192}}`},
		"numeric content length": {"opencode.json", `{"headers":{"Content-Length":"42"}}`},
	}
	for name, tc := range accepted {
		t.Run("accepts "+name, func(t *testing.T) {
			if _, ok := SanitizeConfig(
				[]byte(tc.content),
				SanitizeParams{Path: tc.path, RefuseUndeclaredSecretKeys: true},
			); !ok {
				t.Errorf("SanitizeConfig refused %s", tc.content)
			}
		})
	}
}

func TestNamesAStoredSecret(t *testing.T) {
	for key, want := range map[string]bool{
		"tokens": false, "MAX_TOKENS": false, "token": false, "pass": true, "passwords": true, "dbPass": true,
		"pin": true, "apiKey": true, "model": false,
	} {
		if got := namesAStoredSecret(key); got != want {
			t.Errorf("namesAStoredSecret(%q) = %v, want %v", key, got, want)
		}
	}
}

// TestSanitizeConfigGuardRuleBoundaries pins rules the raw-text scan cannot
// decide: a JSON-escaped token only the decoded walk sees, YAML documents, a
// boolean under a password flag, a quoted number under a token count, and
// references under leaf markers.
// jsonEscapedDigits is "123" written as JSON \u escapes, built from the
// backslash byte so no editor or formatter can unescape it.
var jsonEscapedDigits = string(rune(92)) + "u0031" + string(rune(92)) + "u0032" + string(rune(92)) + "u0033"

func TestSanitizeConfigGuardRuleBoundaries(t *testing.T) {
	refused := map[string]struct{ path, content string }{
		"escaped token":            {"settings.json", `{"x":"Bearer abc` + jsonEscapedDigits + `def456"}`},
		"second document":          {"config.yaml", "model: x\n---\nnote: y\n"},
		"second document, CR-only": {"config.yaml", "model: x\r---\rpassword: hunter2pass\n"},
		"document after a comment": {
			"config.yaml",
			"model: x\n# a note\n---\nnote: y\n",
		},
		"second document without content": {"config.yaml", "model: x\n---\n"},
		"document end marker":             {"config.yaml", "model: x\n...\n"},
		"empty first document":            {"config.yaml", "---\n---\nnote: y\n"},
		"empty first document after a comment": {
			"config.yaml",
			"# header\n---\n---\nnote: y\n",
		},
		"auth literal": {"settings.json", `{"auth":"opaque"}`},
	}
	for name, tc := range refused {
		if _, ok := SanitizeConfig(
			[]byte(tc.content),
			SanitizeParams{Path: tc.path, RefuseUndeclaredSecretKeys: true},
		); ok {
			t.Errorf("%s: SanitizeConfig accepted %s", name, tc.content)
		}
	}
	accepted := map[string]struct{ path, content string }{
		"single yaml document":          {"config.yaml", "model: x\nnote: y\n"},
		"single yaml document, CR-only": {"config.yaml", "model: x\rnote: y\r"},
		"comment then document start": {
			"config.yaml",
			"# agent config\n---\nmodel: x\n",
		},
		"blank lines then document start": {"config.yaml", "\n\n---\nmodel: x\n"},
		"document start alone":            {"config.yaml", "---\nmodel: x\n"},
		"empty document":                  {"config.yaml", "# only a comment\n"},
		"toml string with ---":            {"config.toml", "prompt = \"\"\"\nintro\n---\nmore\n\"\"\"\n"},
		"boolean under password":          {"settings.json", `{"passwordRequired":true,"pin":null}`},
		"quoted token count":              {"settings.json", `{"MAX_TOKENS":"8192"}`},
		"auth reference":                  {"settings.json", `{"auth":"${AUTH}","session":""}`},
	}
	for name, tc := range accepted {
		if _, ok := SanitizeConfig(
			[]byte(tc.content),
			SanitizeParams{Path: tc.path, RefuseUndeclaredSecretKeys: true},
		); !ok {
			t.Errorf("%s: SanitizeConfig refused %s", name, tc.content)
		}
	}
}

// TestSanitizeConfigRefusesANativeScalarCredential pins that a credential the
// parser resolved to a non-string scalar is a raw value like any other: an
// unquoted date under a password, PIN, or key decodes to a timestamp (time.Time
// for YAML and a toml.LocalDate and its siblings for TOML), and the quoted
// spelling of the same value is refused, so the native spelling must be too.
func TestSanitizeConfigRefusesANativeScalarCredential(t *testing.T) {
	refused := map[string]struct{ path, content string }{
		"yaml top-level password": {"config.yaml", "password: 2021-01-01\n"},
		"yaml nested password":    {"config.yaml", "db:\n  password: 2021-01-01\n"},
		"yaml client secret":      {"config.yaml", "client_secret: 2021-01-01\n"},
		"yaml pin":                {"config.yaml", "pin: 2021-01-01\n"},
		"yaml datetime":           {"config.yaml", "pin: 2021-01-01T12:30:00Z\n"},
		"toml password":           {"config.toml", "password = 2021-01-01\n"},
		"toml time":               {"config.toml", "pin = 12:30:00\n"},
		"toml datetime":           {"config.toml", "pin = 2021-01-01T12:30:00Z\n"},
		"name-value pair date":    {"opencode.json", `{"env":[{"name":"DB_PASSWORD","value":20210101}]}`},
		"name-value pair yaml":    {"config.yaml", "env:\n  - name: DB_PASSWORD\n    value: 20210101.0\n"},
		"args list date":          {"config.toml", "[mcp_servers.x]\nargs = [\"--access-token\", 2021-01-01]\n"},
		"args list date joined":   {"config.toml", "args = [\"--access-token=20210101\"]\n"},
	}
	for name, tc := range refused {
		if _, ok := SanitizeConfig(
			[]byte(tc.content),
			SanitizeParams{Path: tc.path, RefuseUndeclaredSecretKeys: true},
		); ok {
			t.Errorf("%s: SanitizeConfig accepted %s", name, tc.content)
		}
	}

	accepted := map[string]struct{ path, content string }{
		"date under a token count":   {"config.yaml", "maxTokens: 2021-01-01\n"},
		"date under an ordinary key": {"config.yaml", "model: 2021-01-01\n"},
	}
	for name, tc := range accepted {
		if _, ok := SanitizeConfig(
			[]byte(tc.content),
			SanitizeParams{Path: tc.path, RefuseUndeclaredSecretKeys: true},
		); !ok {
			t.Errorf("%s: SanitizeConfig refused %s", name, tc.content)
		}
	}
}

// TestSanitizeConfigRefusesAnUnderscoreFlagInACommandString pins that a
// credential flag written with an underscore inside a single command string is
// refused like its hyphenated spelling: the flag and assignment names are
// judged by the same predicate as the args list form, so the two paths agree.
func TestSanitizeConfigRefusesAnUnderscoreFlagInACommandString(t *testing.T) {
	refused := map[string]struct{ path, content string }{
		"api_key flag": {
			"opencode.json",
			`{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"mytool --api_key hunter2pass"}]}]}}`,
		},
		"access_token flag": {
			"opencode.json",
			`{"hooks":{"type":"command","command":"mytool --access_token abcdef"}}`,
		},
		"client_secret flag": {
			"settings.json",
			`{"hooks":{"PreToolUse":[{"command":"mytool --client_secret abcdef"}]}}`,
		},
		"api_key joined": {"opencode.json", `{"permissions":{"allow":["Bash(mytool --api_key=hunter2pass:*)"]}}`},
		"env assignment": {
			"opencode.json",
			`{"hooks":{"type":"command","command":"docker run -e API_KEY=hunter2pass x"}}`,
		},
		"env long flag": {
			"opencode.json",
			`{"hooks":{"type":"command","command":"docker run --env DB_PASSWORD=hunter2pass x"}}`,
		},
	}
	for name, tc := range refused {
		if _, ok := SanitizeConfig(
			[]byte(tc.content),
			SanitizeParams{Path: tc.path, RefuseUndeclaredSecretKeys: true},
		); ok {
			t.Errorf("%s: SanitizeConfig accepted %s", name, tc.content)
		}
	}

	accepted := map[string]struct{ path, content string }{
		"placeholdered flag": {
			"opencode.json",
			`{"hooks":{"type":"command","command":"mytool --api_key ${X_API_KEY}"}}`,
		},
		"harmless environment": {
			"opencode.json",
			`{"hooks":{"type":"command","command":"docker run -e LOG_LEVEL=debug x"}}`,
		},
		"plain command": {
			"opencode.json",
			`{"hooks":{"type":"command","command":"npx -y @modelcontextprotocol/server-filesystem /workspace"}}`,
		},
	}
	for name, tc := range accepted {
		if _, ok := SanitizeConfig(
			[]byte(tc.content),
			SanitizeParams{Path: tc.path, RefuseUndeclaredSecretKeys: true},
		); !ok {
			t.Errorf("%s: SanitizeConfig refused %s", name, tc.content)
		}
	}
}

// TestSanitizeConfigRefusesABareWordInANestedEnvTable pins that a nested table
// or list under an env or header table holds no configuration names, so a bare
// upper-case word inside it is a literal, not a variable name: the same value
// directly in the table is refused, so the nested spelling must be too.
func TestSanitizeConfigRefusesABareWordInANestedEnvTable(t *testing.T) {
	refused := map[string]struct{ path, content string }{
		"nested table bare word":  {"opencode.json", `{"env":{"cfg":{"note":"JBSWY3DPEHPK3PXP"}}}`},
		"nested header list":      {"opencode.json", `{"headers":{"cfg":["ABCDEFGH"]}}`},
		"nested environment word": {"opencode.json", `{"environment":{"prod":{"HOME_LABEL":"PRODUCTION"}}}`},
	}
	for name, tc := range refused {
		if _, ok := SanitizeConfig(
			[]byte(tc.content),
			SanitizeParams{Path: tc.path, RefuseUndeclaredSecretKeys: true},
		); ok {
			t.Errorf("%s: SanitizeConfig accepted %s", name, tc.content)
		}
	}

	accepted := map[string]struct{ path, content string }{
		"nested placeholder":  {"opencode.json", `{"env":{"cfg":{"note":"${X}"}}}`},
		"nested env ref":      {"opencode.json", `{"env":{"cfg":{"note":"{env:X}"}}}`},
		"nested empty string": {"opencode.json", `{"headers":{"cfg":[""]}}`},
	}
	for name, tc := range accepted {
		if _, ok := SanitizeConfig(
			[]byte(tc.content),
			SanitizeParams{Path: tc.path, RefuseUndeclaredSecretKeys: true},
		); !ok {
			t.Errorf("%s: SanitizeConfig refused %s", name, tc.content)
		}
	}
}
