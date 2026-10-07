package configmigration

import (
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strings"
	"unicode"

	"github.com/inoio/agents-sandbox/internal/agent"
)

const (
	authTypeWellKnown     = "wellknown"
	authTypeField         = "type"
	oauthAuthType         = "oauth"
	opencodeOAuthDummyKey = "opencode-oauth-dummy-key"
	githubCopilotProvider = "github-copilot"
)

// LoginProvider is a provider whose credentials are acquired by logging in
// inside the sandbox rather than by migrating a static secret.
type LoginProvider struct {
	Name  string
	Hosts []string
}

func sanitizeAuth(
	data []byte,
	knownHosts, providerHosts map[string]string,
) ([]byte, map[string]Secret, []string, []LoginProvider, error) {
	spec := defaultMigrationSpec()
	return sanitizeAuthWithSpec(data, spec, knownHosts, providerHosts)
}

func sanitizeAuthWithSpec(
	data []byte,
	spec agent.ConfigMigrationSpec,
	knownHosts, providerHosts map[string]string,
) ([]byte, map[string]Secret, []string, []LoginProvider, error) {
	var providers map[string]json.RawMessage
	if err := json.Unmarshal(data, &providers); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("parse auth.json: %w", err)
	}
	result := make(map[string]json.RawMessage)
	secrets := make(map[string]Secret)
	var warnings []string
	var login []LoginProvider
	providerNames := make([]string, 0, len(providers))
	for provider := range providers {
		providerNames = append(providerNames, provider)
	}
	sort.Strings(providerNames)
	for _, provider := range providerNames {
		if providerLogin, isLogin := detectLoginProvider(
			provider,
			providers[provider],
			spec,
			knownHosts,
			providerHosts,
		); isLogin {
			login = append(login, providerLogin)
			continue
		}
		auth, fields, warning := parseAuthEntry(provider, providers[provider], spec.Auth)
		if warning != "" {
			warnings = append(warnings, warning)
			continue
		}
		for _, field := range fields {
			if replaceErr := replaceAuthField(
				auth,
				provider,
				field,
				spec.Auth,
				knownHosts,
				providerHosts,
				secrets,
			); replaceErr != nil {
				return nil, nil, nil, nil, replaceErr
			}
		}
		if err := sanitizeAuthMapWithSpec(
			auth,
			provider,
			"",
			spec.Auth,
			knownHosts,
			providerHosts,
			secrets,
		); err != nil {
			return nil, nil, nil, nil, err
		}
		warnings = append(warnings, unsupportedAuthFieldWarnings(auth, provider, "", spec.Auth)...)
		encoded, err := marshalJSONFn(auth)
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("marshal auth entry %q: %w", provider, err)
		}
		result[provider] = encoded
	}
	if len(result) == 0 {
		return nil, secrets, warnings, login, nil
	}
	encoded, err := marshalJSONIndentFn(result, "", "  ")
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("marshal migrated auth: %w", err)
	}
	return append(encoded, '\n'), secrets, warnings, login, nil
}

func detectLoginProvider(
	provider string,
	data json.RawMessage,
	spec agent.ConfigMigrationSpec,
	knownHosts, providerHosts map[string]string,
) (LoginProvider, bool) {
	var auth map[string]json.RawMessage
	if err := json.Unmarshal(data, &auth); err != nil {
		return LoginProvider{}, false
	}
	if !slices.Contains(loginAuthTypes(spec.Auth), jsonFieldString(auth, authTypeField)) {
		return LoginProvider{}, false
	}
	return LoginProvider{
		Name:  provider,
		Hosts: loginProviderHosts(provider, auth, spec, knownHosts, providerHosts),
	}, true
}

func loginAuthTypes(spec agent.AuthMigrationSpec) []string {
	if len(spec.LoginAuthTypes) > 0 {
		return spec.LoginAuthTypes
	}
	return []string{oauthAuthType}
}

func loginProviderHosts(
	provider string,
	auth map[string]json.RawMessage,
	spec agent.ConfigMigrationSpec,
	knownHosts, providerHosts map[string]string,
) []string {
	if hosts, ok := spec.LoginHosts[provider]; ok {
		return hosts
	}
	return authHosts(provider, auth, knownHosts, providerHosts)
}

func sanitizeAuthMapWithSpec(
	values map[string]json.RawMessage,
	provider, path string,
	spec agent.AuthMigrationSpec,
	knownHosts, providerHosts map[string]string,
	secrets map[string]Secret,
) error {
	for key, raw := range values {
		if err := sanitizeAuthEntry(
			key,
			raw,
			values,
			provider,
			path,
			spec,
			knownHosts,
			providerHosts,
			secrets,
		); err != nil {
			return err
		}
	}
	return nil
}

func sanitizeAuthEntry(
	key string,
	raw json.RawMessage,
	values map[string]json.RawMessage,
	provider, path string,
	spec agent.AuthMigrationSpec,
	knownHosts, providerHosts map[string]string,
	secrets map[string]Secret,
) error {
	fieldPath := key
	if path != "" {
		fieldPath = path + "_" + key
	}
	var nested map[string]json.RawMessage
	if err := json.Unmarshal(raw, &nested); err == nil && nested != nil {
		if err := sanitizeAuthMapWithSpec(
			nested,
			provider,
			fieldPath,
			spec,
			knownHosts,
			providerHosts,
			secrets,
		); err != nil {
			return err
		}
		data, marshalErr := marshalJSONFn(nested)
		if marshalErr != nil {
			return marshalErr
		}
		values[key] = data
		return nil
	}
	return redactAuthField(key, raw, values, provider, path, fieldPath, spec, knownHosts, providerHosts, secrets)
}

func redactAuthField(
	key string,
	raw json.RawMessage,
	values map[string]json.RawMessage,
	provider, path, fieldPath string,
	spec agent.AuthMigrationSpec,
	knownHosts, providerHosts map[string]string,
	secrets map[string]Secret,
) error {
	var text string
	if jsonFieldString(values, "type") == authTypeWellKnown && strings.EqualFold(key, "key") {
		return nil
	}
	if !isJSONString(raw, &text) || isPlaceholderWithAuthSpec(text, spec) {
		return nil
	}
	if spec.RejectUnresolvedValues && isUnresolvedPiConfigValue(text) {
		return fmt.Errorf(
			"auth field %s.%s contains an unresolved environment or command reference",
			provider,
			fieldPath,
		)
	}
	if !isSensitiveField(key, spec.SensitiveFields) {
		if spec.RejectUnresolvedValues && isCredentialContainerPath(path) && text != "" {
			return fmt.Errorf("auth field %s.%s contains an unsupported environment override", provider, fieldPath)
		}
		return nil
	}
	name := spec.SecretPrefix + "_" + secretPart(provider+"_"+fieldPath)
	if existing, ok := secrets[name]; ok && existing.Value != text {
		return fmt.Errorf("secret name collision for %s", name)
	}
	secrets[name] = Secret{ //nolint:exhaustruct // auth migrations only set value and inferred hosts
		Value: text,
		Hosts: authHosts(provider, values, knownHosts, providerHosts),
	}
	encoded, marshalErr := marshalJSONFn(spec.AuthPlaceholderPrefix + name)
	if marshalErr != nil {
		return marshalErr
	}
	values[key] = encoded
	return nil
}

func isJSONString(raw json.RawMessage, target *string) bool {
	return json.Unmarshal(raw, target) == nil
}

func unsupportedAuthFieldWarnings(
	values map[string]json.RawMessage,
	provider, path string,
	spec agent.AuthMigrationSpec,
) []string {
	var warnings []string
	for key, raw := range values {
		fieldPath := key
		if path != "" {
			fieldPath = path + "." + key
		}
		var nested map[string]json.RawMessage
		if err := json.Unmarshal(raw, &nested); err == nil && nested != nil {
			warnings = append(warnings, unsupportedAuthFieldWarnings(nested, provider, fieldPath, spec)...)
			continue
		}
		if key == authTypeField || isSensitiveField(key, spec.SensitiveFields) || isSafeField(key, spec.SafeFields) {
			continue
		}
		var text string
		if json.Unmarshal(raw, &text) == nil && text != "" &&
			!isPlaceholderWithAuthSpec(text, spec) {
			warnings = append(warnings, fmt.Sprintf(
				"review required: auth field %s.%s is not recognized as safe or sensitive; inspect the generated file and submit an issue if automatic migration should support it",
				provider,
				fieldPath,
			))
		}
	}
	return warnings
}

func parseAuthEntry(
	provider string,
	data json.RawMessage,
	spec agent.AuthMigrationSpec,
) (map[string]json.RawMessage, []string, string) {
	var auth map[string]json.RawMessage
	if err := json.Unmarshal(data, &auth); err != nil {
		return nil, nil, fmt.Sprintf("skipped malformed auth entry %q", provider)
	}
	var authType string
	if err := json.Unmarshal(auth["type"], &authType); err != nil {
		return nil, nil, fmt.Sprintf("skipped auth entry %q without a supported type", provider)
	}
	fields := authFieldsFor(spec, authType)
	if len(fields) == 0 {
		return nil, nil, fmt.Sprintf("skipped unsupported auth type %q for provider %q", authType, provider)
	}
	return auth, fields, ""
}

func replaceAuthField(
	auth map[string]json.RawMessage,
	provider, field string,
	spec agent.AuthMigrationSpec,
	knownHosts, providerHosts map[string]string,
	secrets map[string]Secret,
) error {
	raw, exists := auth[field]
	if !exists {
		return nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("auth field %s.%s is not a string", provider, field)
	}
	if isPlaceholderWithAuthSpec(value, spec) || value == opencodeOAuthDummyKey {
		return nil
	}
	if spec.RejectUnresolvedValues && isUnresolvedPiConfigValue(value) {
		return fmt.Errorf("auth field %s.%s contains an unresolved environment or command reference", provider, field)
	}
	name := spec.SecretPrefix + "_" + secretPart(provider) + "_" + strings.ToUpper(field)
	if existing, ok := secrets[name]; ok && existing.Value != value {
		return fmt.Errorf("secret name collision for %s", name)
	}
	secrets[name] = Secret{ //nolint:exhaustruct // auth migrations only set value and inferred hosts
		Value: value,
		Hosts: authHosts(provider, auth, knownHosts, providerHosts),
	}
	auth[field] = json.RawMessage(fmt.Sprintf("%q", spec.AuthPlaceholderPrefix+name))
	return nil
}

func jsonFieldString(values map[string]json.RawMessage, key string) string {
	var value string
	_ = json.Unmarshal(values[key], &value)
	return value
}

func authHosts(provider string, auth map[string]json.RawMessage, knownHosts, providerHosts map[string]string) []string {
	if provider == githubCopilotProvider {
		if enterprise := jsonFieldString(auth, "enterpriseUrl"); enterprise != "" {
			if parsed, err := url.Parse(enterprise); err == nil && parsed.Hostname() != "" {
				return []string{"copilot-api." + parsed.Hostname()}
			}
		}
	}
	if jsonFieldString(auth, "type") == authTypeWellKnown {
		if parsed, err := url.Parse(provider); err == nil && parsed.Hostname() != "" {
			return []string{parsed.Hostname()}
		}
	}
	if host := providerHosts[strings.ToLower(provider)]; host != "" {
		return []string{host}
	}
	return knownHost(provider, knownHosts)
}

func authFields(authType string) []string {
	switch authType {
	case oauthAuthType:
		return []string{"access", "refresh"}
	case "api":
		return []string{"key"}
	case authTypeWellKnown:
		return []string{"token"}
	default:
		return nil
	}
}

func authFieldsFor(spec agent.AuthMigrationSpec, authType string) []string {
	if fields := spec.AuthFields[authType]; len(fields) > 0 {
		return fields
	}
	return authFields(authType)
}

func isSensitiveField(key string, fields []string) bool {
	normalized := strings.ToLower(strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return -1
	}, key))
	for _, field := range fields {
		if normalized == strings.ToLower(strings.Map(func(r rune) rune {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				return r
			}
			return -1
		}, field)) {
			return true
		}
	}
	return false
}

func isSafeField(key string, fields []string) bool {
	normalized := strings.ToLower(strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return -1
	}, key))
	for _, field := range fields {
		if normalized == strings.ToLower(field) {
			return true
		}
	}
	return false
}

func isPlaceholderWithSpec(value string, spec agent.ConfigMigrationSpec) bool {
	return strings.Contains(value, spec.Auth.AuthPlaceholderPrefix) ||
		strings.Contains(value, spec.Auth.ConfigPlaceholderPrefix)
}

func isPlaceholderWithAuthSpec(value string, spec agent.AuthMigrationSpec) bool {
	return strings.Contains(value, spec.AuthPlaceholderPrefix) || strings.Contains(value, spec.ConfigPlaceholderPrefix)
}

func secretPart(value string) string {
	var b strings.Builder
	lastUnderscore := false
	for _, r := range strings.ToUpper(value) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			lastUnderscore = false
		} else if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	return strings.Trim(b.String(), "_")
}

func knownHost(provider string, knownHosts map[string]string) []string {
	host := knownHosts[strings.ToLower(provider)]
	if host == "" {
		return nil
	}
	return []string{host}
}
