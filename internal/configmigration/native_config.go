package configmigration

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/titanous/json5"
	"gopkg.in/yaml.v3"

	"github.com/inoio/agents-sandbox/internal/agent"
)

const (
	endpointKey              = "endpoint"
	urlKey                   = "url"
	providerKey              = "provider"
	secretKey                = "secret"
	baseURLKey               = "baseURL"
	claudeAnthropicAPIKeyEnv = "ANTHROPIC_" + "API_KEY"
	claudeAnthropicAPIHost   = "api.anthropic.com"
)

func migrateNativeConfig(
	hostHome string,
	spec agent.ConfigMigrationSpec,
) ([]byte, map[string]Secret, []string, map[string]string, error) {
	configDir, _ := nativeAgentPaths(hostHome, spec)
	var merged map[string]any
	var warnings []string
	for _, name := range spec.NativeConfigFiles {
		data, err := os.ReadFile(filepath.Join(configDir, name))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, nil, nil, nil, fmt.Errorf("read native config %s: %w", name, err)
		}
		parsed, parseErr := parseNativeConfigWithStrictJSON(name, data, spec.NativeConfigStrictJSON)
		if parseErr != nil {
			if spec.NativeConfigStrictJSON {
				return nil, nil, nil, nil, fmt.Errorf("parse native config %s: %w", name, parseErr)
			}
			warnings = append(warnings, fmt.Sprintf("skipped malformed native config %q", name))
			continue
		}
		merged = mergeConfigMaps(merged, parsed)
	}
	if len(merged) == 0 {
		return nil, nil, warnings, nil, nil
	}
	secrets := make(map[string]Secret)
	providerHosts := providerEndpointHostsWithSpec(merged, spec.Auth.EndpointFields)
	if host := findEndpointHost(merged, spec.Auth.EndpointFields); host != "" {
		providerHosts[""] = host
	}
	if sanitizeErr := sanitizeConfigMapWithSpec(
		merged,
		"",
		"",
		spec.Auth,
		spec.KnownProviderHosts,
		providerHosts,
		secrets,
	); sanitizeErr != nil {
		return nil, nil, nil, nil, sanitizeErr
	}
	data, err := marshalJSONIndentFn(merged, "", "  ")
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("marshal migrated agent config: %w", err)
	}
	return append(data, '\n'), secrets, warnings, providerHosts, nil
}

func migrateNativeSupplementalConfig(
	hostHome string,
	spec agent.ConfigMigrationSpec,
) (map[string][]byte, map[string]Secret, []string, map[string]string, error) {
	configDir, _ := nativeAgentPaths(hostHome, spec)
	files := make(map[string][]byte)
	secrets := make(map[string]Secret)
	var warnings []string
	providerHosts := make(map[string]string)
	for index, name := range spec.NativeSupplementalFiles {
		data, err := os.ReadFile(filepath.Join(configDir, name))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, nil, nil, nil, fmt.Errorf("read native config %s: %w", name, err)
		}
		parsed, parseErr := parseNativeConfig(name, data)
		if parseErr != nil {
			warnings = append(warnings, fmt.Sprintf("skipped malformed native config %q", name))
			continue
		}
		fileSecrets := make(map[string]Secret)
		fileHosts := providerEndpointHostsWithSpec(parsed, spec.Auth.EndpointFields)
		if sanitizeErr := sanitizeConfigMapWithSpec(
			parsed,
			"",
			"",
			spec.Auth,
			spec.KnownProviderHosts,
			fileHosts,
			fileSecrets,
		); sanitizeErr != nil {
			return nil, nil, nil, nil, sanitizeErr
		}
		if err := mergePlannedSecrets(secrets, fileSecrets); err != nil {
			return nil, nil, nil, nil, err
		}
		maps.Copy(providerHosts, fileHosts)
		encoded, marshalErr := marshalJSONIndentFn(parsed, "", "  ")
		if marshalErr != nil {
			return nil, nil, nil, nil, fmt.Errorf("marshal migrated agent config %q: %w", name, marshalErr)
		}
		managedName := name
		if index < len(spec.ManagedSupplementalFiles) && spec.ManagedSupplementalFiles[index] != "" {
			managedName = spec.ManagedSupplementalFiles[index]
		}
		files[managedName] = append(encoded, '\n')
	}
	return files, secrets, warnings, providerHosts, nil
}

func parseNativeConfig(name string, data []byte) (map[string]any, error) {
	return parseNativeConfigWithStrictJSON(name, data, false)
}

func parseNativeConfigWithStrictJSON(name string, data []byte, strictJSON bool) (map[string]any, error) {
	var result map[string]any
	switch strings.ToLower(filepath.Ext(name)) {
	case extYAML, extYML:
		if err := yaml.Unmarshal(data, &result); err != nil {
			return nil, err
		}
	default:
		var err error
		if strictJSON {
			err = json.Unmarshal(data, &result)
		} else {
			err = json5.Unmarshal(data, &result)
		}
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func mergeConfigMaps(base, override map[string]any) map[string]any {
	if base == nil {
		base = make(map[string]any)
	}
	result := make(map[string]any, len(base)+len(override))
	maps.Copy(result, base)
	for key, value := range override {
		baseMap, baseOK := result[key].(map[string]any)
		overrideMap, overrideOK := value.(map[string]any)
		if baseOK && overrideOK {
			result[key] = mergeConfigMaps(baseMap, overrideMap)
		} else {
			result[key] = value
		}
	}
	return result
}

func sanitizeConfigMapWithSpec(
	values map[string]any,
	provider string,
	path string,
	spec agent.AuthMigrationSpec,
	knownHosts, providerHosts map[string]string,
	secrets map[string]Secret,
) error {
	for key, value := range values {
		currentPath := key
		if path != "" {
			currentPath = path + "_" + key
		}
		handled, err := sanitizeNestedConfigValue(
			key,
			value,
			provider,
			path,
			currentPath,
			spec,
			knownHosts,
			providerHosts,
			secrets,
		)
		if err != nil {
			return err
		}
		if handled {
			continue
		}
		if err := sanitizeConfigField(
			values,
			key,
			value,
			provider,
			path,
			currentPath,
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

func sanitizeNestedConfigValue(
	key string,
	value any,
	provider, path, currentPath string,
	spec agent.AuthMigrationSpec,
	knownHosts, providerHosts map[string]string,
	secrets map[string]Secret,
) (bool, error) {
	if nested, ok := value.(map[string]any); ok {
		nestedProvider := provider
		if path == "" && (key == providerKey || key == "providers") {
			nestedProvider = ""
		}
		if path == providerKey || path == "providers" {
			nestedProvider = key
		}
		if err := sanitizeConfigMapWithSpec(
			nested,
			nestedProvider,
			currentPath,
			spec,
			knownHosts,
			providerHosts,
			secrets,
		); err != nil {
			return true, err
		}
		return true, nil
	}
	if array, ok := value.([]any); ok {
		for index, item := range array {
			if nested, nestedOK := item.(map[string]any); nestedOK {
				if err := sanitizeConfigMapWithSpec(
					nested,
					provider,
					fmt.Sprintf("%s_%d", currentPath, index),
					spec,
					knownHosts,
					providerHosts,
					secrets,
				); err != nil {
					return true, err
				}
			}
		}
		return true, nil
	}
	return false, nil
}

func sanitizeConfigField(
	values map[string]any,
	key string,
	value any,
	provider, path, currentPath string,
	spec agent.AuthMigrationSpec,
	knownHosts, providerHosts map[string]string,
	secrets map[string]Secret,
) error {
	text, isText := value.(string)
	if isText && spec.RejectUnresolvedValues {
		if err := rejectUnresolvedConfigField(
			values,
			key,
			text,
			provider,
			path,
			currentPath,
			spec,
			knownHosts,
			providerHosts,
			secrets,
		); err != nil {
			return err
		}
		if isClaudeEndpointVariable(key) || claudeSecretVariable(key) || isUnresolvedPiConfigValue(text) {
			return nil
		}
	}
	if !isText || !isSensitiveField(key, spec.ConfigSensitiveFields) ||
		isPlaceholderWithAuthSpec(text, spec) {
		if isText && isEndpointField(key, spec.EndpointFields) {
			if err := rejectEmbeddedEndpointCredential(text); err != nil {
				return fmt.Errorf("config field %s: %w", currentPath, err)
			}
		}
		return nil
	}
	name := spec.SecretPrefix + "_CONFIG_" + secretPart(currentPath)
	if existing, exists := secrets[name]; exists && existing.Value != text {
		name += "_" + shortHash(text)
	}
	secrets[name] = Secret{ //nolint:exhaustruct // config migrations only set value and inferred hosts
		Value: text,
		Hosts: configSecretHosts(provider, values, knownHosts, providerHosts),
	}
	values[key] = spec.ConfigPlaceholderPrefix + name + spec.ConfigPlaceholderSuffix
	return nil
}

func rejectUnresolvedConfigField(
	values map[string]any,
	key, text, provider, path, currentPath string,
	spec agent.AuthMigrationSpec,
	knownHosts, providerHosts map[string]string,
	secrets map[string]Secret,
) error {
	if isUnresolvedPiConfigValue(text) {
		return fmt.Errorf("config field %s contains an unresolved environment or command reference", currentPath)
	}
	if isClaudeEndpointVariable(key) {
		if err := rejectEmbeddedEndpointCredential(text); err != nil {
			return fmt.Errorf("config field %s: %w", currentPath, err)
		}
		return nil
	}
	if isClaudeUnsafeSettingKey(key) {
		return fmt.Errorf("config field %s contains an unsupported credential helper or command", currentPath)
	}
	if claudeSecretVariable(key) {
		return migrateClaudeSecretField(
			values,
			key,
			text,
			provider,
			spec,
			knownHosts,
			providerHosts,
			secrets,
		)
	}
	if isCredentialContainerPath(path) &&
		!isPlaceholderWithAuthSpec(text, spec) && !isClaudeEndpointVariable(key) {
		return fmt.Errorf("config field %s contains an unsupported credential or header override", currentPath)
	}
	return nil
}

func migrateClaudeSecretField(
	values map[string]any,
	key, text, provider string,
	spec agent.AuthMigrationSpec,
	knownHosts, providerHosts map[string]string,
	secrets map[string]Secret,
) error {
	name := spec.SecretPrefix + "_ENV_" + secretPart(key)
	if existing, exists := secrets[name]; exists && existing.Value != text {
		return fmt.Errorf("secret name collision for %s", name)
	}
	hosts := configSecretHosts(provider, values, knownHosts, providerHosts)
	if isClaudeEndpointVariable(key) {
		if parsed, parseErr := url.Parse(text); parseErr == nil && parsed.Hostname() != "" {
			hosts = []string{parsed.Hostname()}
		}
	}
	if len(hosts) == 0 {
		hosts = knownHost("anthropic", knownHosts)
	}
	secrets[name] = Secret{Value: text, Host: "", Hosts: hosts, AllowAnyHostDangerous: false}
	values[key] = spec.ConfigPlaceholderPrefix + name + spec.ConfigPlaceholderSuffix
	return nil
}

func isCredentialContainerPath(path string) bool {
	normalized := strings.ToLower(path)
	return normalized == "env" || strings.HasSuffix(normalized, "_env") ||
		normalized == "headers" || strings.HasSuffix(normalized, "_headers")
}

func claudeSecretVariable(value string) bool {
	switch strings.ToUpper(value) {
	case claudeAnthropicAPIKeyEnv, "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN",
		"ANTHROPIC_AWS_API_KEY", "ANTHROPIC_FOUNDRY_API_KEY", "ANTHROPIC_FOUNDRY_AUTH_TOKEN",
		"ANTHROPIC_IDENTITY_TOKEN", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN",
		"AWS_BEARER_TOKEN_BEDROCK":
		return true
	default:
		return false
	}
}

func isClaudeEndpointVariable(value string) bool {
	return strings.EqualFold(value, "ANTHROPIC_BASE_URL") ||
		strings.EqualFold(value, "ANTHROPIC_BEDROCK_BASE_URL") ||
		strings.EqualFold(value, "ANTHROPIC_FOUNDRY_BASE_URL") ||
		strings.EqualFold(value, "ANTHROPIC_VERTEX_BASE_URL")
}

func isClaudeUnsafeSettingKey(value string) bool {
	switch strings.ToLower(strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return -1
	}, value)) {
	case "apikeyhelper", "awsauthrefresh", "awscredentialexport", "gpcauthrefresh":
		return true
	default:
		return false
	}
}

func configSecretHosts(
	provider string,
	values map[string]any,
	knownHosts, providerHosts map[string]string,
) []string {
	if host := endpointHostFromValues(values); host != "" {
		return []string{host}
	}
	if provider == "" {
		if host := providerHosts[""]; host != "" {
			return []string{host}
		}
	}
	if host := knownHosts[strings.ToLower(provider)]; host != "" {
		return []string{host}
	}
	return knownHost(provider, providerHosts)
}

func endpointHostFromValues(values map[string]any) string {
	for key, raw := range values {
		if !isClaudeEndpointVariable(key) {
			continue
		}
		if text, ok := raw.(string); ok {
			if parsed, err := url.Parse(text); err == nil && parsed.Hostname() != "" {
				return parsed.Hostname()
			}
		}
	}
	for _, key := range []string{baseURLKey, "baseUrl", endpointKey, urlKey, "enterpriseUrl"} {
		if raw, ok := values[key].(string); ok {
			if parsed, err := url.Parse(raw); err == nil && parsed.Hostname() != "" {
				return parsed.Hostname()
			}
		}
	}
	return ""
}

func findEndpointHost(value any, endpointFields []string) string {
	switch current := value.(type) {
	case map[string]any:
		return findEndpointHostInMap(current, endpointFields)
	case []any:
		return findEndpointHostInSlice(current, endpointFields)
	default:
		return ""
	}
}

func findEndpointHostInMap(values map[string]any, endpointFields []string) string {
	for key, nested := range values {
		if isEndpointField(key, endpointFields) {
			if text, ok := nested.(string); ok {
				if parsed, err := url.Parse(text); err == nil && parsed.Hostname() != "" {
					return parsed.Hostname()
				}
			}
		}
		if host := findEndpointHost(nested, endpointFields); host != "" {
			return host
		}
	}
	return ""
}

func findEndpointHostInSlice(values []any, endpointFields []string) string {
	for _, nested := range values {
		if host := findEndpointHost(nested, endpointFields); host != "" {
			return host
		}
	}
	return ""
}

func providerEndpointHostsWithSpec(values map[string]any, endpointFields []string) map[string]string {
	result := make(map[string]string)
	providers, ok := values["provider"].(map[string]any)
	if !ok {
		providers, ok = values["providers"].(map[string]any)
	}
	if !ok {
		return result
	}
	for provider, raw := range providers {
		config, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		options, _ := config["options"].(map[string]any)
		for _, key := range endpointFields {
			endpoint, endpointOK := config[key].(string)
			if !endpointOK && options != nil {
				endpoint, endpointOK = options[key].(string)
			}
			if endpointOK {
				if parsed, err := url.Parse(endpoint); err == nil && parsed.Hostname() != "" {
					result[strings.ToLower(provider)] = parsed.Hostname()
					break
				}
			}
		}
	}
	return result
}

func isEndpointField(key string, endpointFields []string) bool {
	switch strings.ToLower(key) {
	case "baseurl", endpointKey, urlKey, "enterpriseurl":
		for _, field := range endpointFields {
			if strings.EqualFold(key, field) {
				return true
			}
		}
	}
	return false
}

func rejectEmbeddedEndpointCredential(value string) error {
	parsed, err := url.Parse(value)
	if err != nil {
		return errors.New("invalid endpoint URL")
	}
	if parsed.User != nil {
		return errors.New("endpoint contains URL credentials")
	}
	for key := range parsed.Query() {
		switch strings.ToLower(strings.ReplaceAll(key, "-", "_")) {
		case "api_key", "apikey", "key", "token", "access_token", "signature", "sig", "password", secretKey:
			return errors.New("endpoint contains credential query parameters")
		}
	}
	return nil
}

func mergePlannedSecrets(base, additions map[string]Secret) error {
	for name, secret := range additions {
		if existing, ok := base[name]; ok && existing.Value != secret.Value {
			return fmt.Errorf("secret name collision for %s", name)
		}
		base[name] = secret
	}
	return nil
}

func isUnresolvedPiConfigValue(value string) bool {
	return strings.HasPrefix(value, "!") ||
		(strings.HasPrefix(value, "$") && !strings.HasPrefix(value, "$MSB_"))
}
