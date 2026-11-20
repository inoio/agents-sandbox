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
		{name: "colon with no key", content: `: "sk"`, value: "sk", want: "", ok: false},
		{name: "equals with no key", content: `= "sk"`, value: "sk", want: "", ok: false},
		{name: "value at content start", content: `"sk"`, value: "sk", want: "", ok: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			start, end, ok := assignmentKeyRange(test.content, strings.Index(test.content, test.value))
			require.Equal(t, test.ok, ok)
			if test.ok {
				require.Equal(t, test.want, test.content[start:end])
			}
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
		`{"secrets":[4096]}`,
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
	require.True(t, holdsRawSecretString(map[any]any{1: "sk-raw"}))
	require.False(t, holdsRawSecretString(map[any]any{1: map[string]any{"a": "${REF}"}}))
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

// TestDecodedHoldsUndeclaredSecretValueAnyMapIgnoresOrdinaryKeys pins the
// map[any]any branch's key test: a string key that is not secret-shaped does not
// leak even when it holds a raw string.
func TestDecodedHoldsUndeclaredSecretValueAnyMapIgnoresOrdinaryKeys(t *testing.T) {
	require.False(t, decodedHoldsUndeclaredSecretValue(map[any]any{"model": "raw"}))
}
