package viperconfig

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/spf13/viper"

	"github.com/inoio/agents-sandbox/internal/notify"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
	"github.com/inoio/agents-sandbox/internal/sandbox/ports"
	"github.com/inoio/agents-sandbox/internal/upgrade"
)

func validate(v *viper.Viper) error {
	// prune-age validation does not gate on cpus being set, so run it
	// before the cpus early-exit below.
	if err := validatePruneAges(v); err != nil {
		return err
	}
	if err := validateAutoStop(v); err != nil {
		return err
	}
	if err := validateAutoStopTimeout(v); err != nil {
		return err
	}
	if err := validateMaxLifetime(v); err != nil {
		return err
	}
	if err := validateReadyTimeouts(v); err != nil {
		return err
	}
	if err := normalizeNetworkProfile(v); err != nil {
		return err
	}
	if err := validateNetworkDNSServers(v); err != nil {
		return err
	}
	if err := validateNetworkInterceptTLS(v); err != nil {
		return err
	}
	if err := validateNetworkTLSBypass(v); err != nil {
		return err
	}
	if err := validateNetworkProxy(v); err != nil {
		return err
	}
	if err := validatePorts(v); err != nil {
		return err
	}
	if err := validateHomeVolume(v); err != nil {
		return err
	}
	if err := validateUpgrade(v); err != nil {
		return err
	}
	if err := validateNotify(v); err != nil {
		return err
	}
	// gomutants:disable-next-line BRANCH_IF reason="the fall-through checks GetInt's zero value, which is in range"
	if !v.IsSet(keyCPUs) {
		return nil
	}
	cpus := v.GetInt(keyCPUs)
	if cpus < 0 || cpus > 255 {
		return fmt.Errorf("launcher config cpus must be between 0 and 255, got %d", cpus)
	}
	return nil
}

// normalizeNetworkProfile validates network.profile and stores the canonical
// profile list back on the viper key. A scalar, comma-separated, or list value
// with surrounding spaces (e.g. "public, host") validates fine, but would
// otherwise decode with the spaces intact and be rejected by the microsandbox
// SDK at VM creation. Storing the parsed result keeps decode in agreement with
// validation.
func normalizeNetworkProfile(v *viper.Viper) error {
	if !v.IsSet(keyNetworkProfile) {
		return nil
	}
	entries, err := profileEntries(v.Get(keyNetworkProfile))
	if err != nil {
		return fmt.Errorf("launcher config %s: %w", keyNetworkProfile, err)
	}
	profiles, err := network.ParseProfiles(entries)
	if err != nil {
		return err
	}
	v.Set(keyNetworkProfile, profiles)
	return nil
}

// validateNetworkDNSServers rejects malformed network.dns-servers values at
// config-load time. The raw value may be a comma-separated string (env/flag) or
// a YAML/JSON list (config file).
func validateNetworkDNSServers(v *viper.Viper) error {
	if !v.IsSet(keyNetworkDNSServers) {
		return nil
	}
	entries, err := dnsServerEntries(v.Get(keyNetworkDNSServers))
	if err != nil {
		return fmt.Errorf("launcher config %s: %w", keyNetworkDNSServers, err)
	}
	if _, err := network.NormalizeDNSServers(entries); err != nil {
		return fmt.Errorf("launcher config %s: %w", keyNetworkDNSServers, err)
	}
	return nil
}

// validateNetworkInterceptTLS rejects an unknown network.intercept-tls mode at
// config-load time. An unset key reads as the empty string and an empty value is
// auto, so both validate without an IsSet guard.
func validateNetworkInterceptTLS(v *viper.Viper) error {
	if _, err := network.ParseInterceptMode(v.GetString(keyNetworkInterceptTLS)); err != nil {
		return fmt.Errorf("launcher config %s: %w", keyNetworkInterceptTLS, err)
	}
	return nil
}

// validateNetworkTLSBypass rejects empty network.tls-bypass entries at
// config-load time. The raw value may be a comma-separated string (config file
// scalar) or a YAML/JSON list.
func validateNetworkTLSBypass(v *viper.Viper) error {
	if !v.IsSet(keyNetworkTLSBypass) {
		return nil
	}
	entries, err := stringListEntries(v.Get(keyNetworkTLSBypass), "a list of hosts")
	if err != nil {
		return fmt.Errorf("launcher config %s: %w", keyNetworkTLSBypass, err)
	}
	if _, err := network.ValidateTLSBypass(entries); err != nil {
		return fmt.Errorf("launcher config %s: %w", keyNetworkTLSBypass, err)
	}
	return nil
}

// validateNetworkProxy rejects a malformed network.proxy block at config-load
// time: an unknown protocol, a non-host:port address, credentials a protocol
// cannot carry (socks4), or a lone username/password-env. The block is decoded
// after validation, so the keys are read individually; an unset block is the
// zero proxy and passes. The check runs here so a SOCKS4-with-credentials
// configuration names the protocol at load rather than failing at VM creation.
func validateNetworkProxy(v *viper.Viper) error {
	proxy := network.Proxy{
		Protocol:    network.ProxyProtocol(v.GetString(keyNetworkProxyProtocol)),
		Address:     v.GetString(keyNetworkProxyAddress),
		Username:    v.GetString(keyNetworkProxyUsername),
		PasswordEnv: v.GetString(keyNetworkProxyPasswordEnv),
	}
	// Policy.Validate is the network layer's single well-formedness entry point;
	// a zero-valued policy validates the proxy alone.
	//exhaustruct:ignore // the other policy fields are deliberately zero here
	if err := (network.Policy{Proxy: proxy}).Validate(); err != nil {
		return fmt.Errorf("launcher config %s: %w", keyNetworkProxyPrefix, err)
	}
	return nil
}

// validatePorts rejects malformed ports values at config-load time. The raw
// value may be a comma-separated string (env/flag) or a YAML/JSON list
// (config file).
func validatePorts(v *viper.Viper) error {
	if !v.IsSet(keyPorts) {
		return nil
	}
	entries, err := stringListEntries(v.Get(keyPorts), "a list of port specs")
	if err != nil {
		return fmt.Errorf("launcher config %s: %w", keyPorts, err)
	}
	if _, err := ports.ParsePortMappings(entries); err != nil {
		return fmt.Errorf("launcher config %s: %w", keyPorts, err)
	}
	return nil
}

// dnsServerEntries converts a raw network.dns-servers value (comma-separated
// string or list) into the individual entries.
func dnsServerEntries(raw any) ([]string, error) {
	return stringListEntries(raw, "a list of DNS servers")
}

// stringListEntries converts a raw config value (comma-separated string or
// YAML/JSON list) into the individual string entries.
func stringListEntries(raw any, what string) ([]string, error) {
	switch val := raw.(type) {
	case string:
		if val == "" {
			return nil, errors.New("must not be empty")
		}
		return strings.Split(val, ","), nil
	case []string:
		return val, nil
	case []any:
		entries := make([]string, 0, len(val))
		for _, e := range val {
			s, ok := e.(string)
			if !ok {
				return nil, fmt.Errorf("entries must be strings, got %T", e)
			}
			entries = append(entries, s)
		}
		return entries, nil
	default:
		return nil, fmt.Errorf("must be %s, got %T", what, raw)
	}
}

// profileEntries converts a raw network.profile value (comma-separated string
// or list) into the individual profile names.
func profileEntries(raw any) ([]string, error) {
	switch val := raw.(type) {
	case string:
		if val == "" {
			return nil, errors.New("must not be empty")
		}
		return strings.Split(val, ","), nil
	case []string:
		return val, nil
	case []any:
		entries := make([]string, 0, len(val))
		for _, e := range val {
			s, ok := e.(string)
			if !ok {
				return nil, fmt.Errorf("entries must be strings, got %T", e)
			}
			entries = append(entries, s)
		}
		return entries, nil
	default:
		return nil, fmt.Errorf("must be a profile name or a list of profile names, got %T", raw)
	}
}

// homeVolumeOnImageChangeValues are the answers home-volume.on-image-change
// accepts.
var homeVolumeOnImageChangeValues = []string{"keep", "migrate", "reset", "ask"}

// validateHomeVolume validates the home-volume.on-image-change key.
func validateHomeVolume(v *viper.Viper) error {
	if !v.IsSet(KeyHomeVolumeOnImageChange) {
		return nil
	}
	value := v.GetString(KeyHomeVolumeOnImageChange)
	if !slices.Contains(homeVolumeOnImageChangeValues, value) {
		return fmt.Errorf("invalid %s %q (want %s)",
			KeyHomeVolumeOnImageChange, value, strings.Join(homeVolumeOnImageChangeValues, ", "))
	}
	return nil
}

// validateUpgrade validates the upgrade.mode, upgrade.interval and
// upgrade.channel config keys.
func validateUpgrade(v *viper.Viper) error {
	if v.IsSet(keyUpgradeMode) {
		if _, err := upgrade.ParseMode(v.GetString(keyUpgradeMode)); err != nil {
			return err
		}
	}
	if v.IsSet(keyUpgradeChannel) {
		if _, err := upgrade.ParseChannel(v.GetString(keyUpgradeChannel)); err != nil {
			return err
		}
	}
	if !v.IsSet(keyUpgradeInterval) {
		return nil
	}
	d := v.GetDuration(keyUpgradeInterval)
	if s, ok := v.Get(keyUpgradeInterval).(string); ok {
		if parsed, ok := ParseHumanDuration(s); ok {
			d = parsed
		}
	}
	if d < upgrade.MinInterval {
		return fmt.Errorf("launcher config %s must be >= %s, got %v", keyUpgradeInterval, upgrade.MinInterval, d)
	}
	return nil
}

func validateNotify(v *viper.Viper) error {
	if !v.IsSet(keyNotifyAudio) {
		return nil
	}
	if _, err := notify.ParseAudioMode(v.GetString(keyNotifyAudio)); err != nil {
		return err
	}
	return nil
}

func validatePruneAges(v *viper.Viper) error {
	for _, key := range []string{keyAutoPruneAge, keyManualPruneAge} {
		if !v.IsSet(key) {
			continue
		}
		d := v.GetDuration(key)
		// GetDuration yields whole nanoseconds, so only d == 1ns differs; the
		// fall-through then re-parses the raw string and still finds it > 0.
		if d > 0 { // gomutants:disable INTEGER_INCREMENT,BRANCH_IF reason="only d == 1ns differs and the fall-through leaves it positive"
			continue
		}
		// viper's GetDuration returned 0 — check if the raw value is a
		// "7d"-style string that the decode hook failed to convert.
		if s, ok := v.Get(key).(string); ok {
			if parsed, ok := ParseHumanDuration(s); ok {
				d = parsed
			}
		}
		// Reaching here means d <= 0 already (the d > 0 guard above), so a
		// bound of 1 can never differ from 0.
		if d <= 0 { // gomutants:disable INTEGER_INCREMENT reason="d is already <= 0 at this point, so a bound of 1 is equivalent"
			return fmt.Errorf("launcher config %s must be > 0, got %v", key, d)
		}
	}
	return nil
}

func validateAutoStop(v *viper.Viper) error {
	// gomutants:disable-next-line BRANCH_IF reason="the fall-through checks GetInt's zero value, which is >= 0"
	if !v.IsSet(keyAutoStopMaxSessionRetries) {
		return nil
	}
	retries := v.GetInt(keyAutoStopMaxSessionRetries)
	if retries >= 0 {
		return nil
	}
	return fmt.Errorf("launcher config %s must be >= 0, got %d", keyAutoStopMaxSessionRetries, retries)
}

func validateAutoStopTimeout(v *viper.Viper) error {
	if !v.IsSet(keyAutoStopTimeout) {
		return nil
	}
	d := v.GetDuration(keyAutoStopTimeout)
	// GetDuration yields whole nanoseconds, so only d == 1ns differs; the
	// fall-through then re-parses the raw string and still finds it > 0.
	if d > 0 { // gomutants:disable INTEGER_INCREMENT,BRANCH_IF reason="only d == 1ns differs and the fall-through leaves it positive"
		return nil
	}
	if s, ok := v.Get(keyAutoStopTimeout).(string); ok {
		if parsed, ok := ParseHumanDuration(s); ok {
			d = parsed
		}
	}
	// Reaching here means d <= 0 already (the d > 0 guard above), so a bound
	// of 1 can never differ from 0.
	if d <= 0 { // gomutants:disable INTEGER_INCREMENT reason="d is already <= 0 at this point, so a bound of 1 is equivalent"
		return fmt.Errorf("launcher config %s must be > 0, got %v", keyAutoStopTimeout, d)
	}
	return nil
}

// validateMaxLifetime rejects a set max-lifetime the SDK's lifecycle validation
// would reject at create (ErrInvalidConfig): it must be a positive whole number
// of seconds. Unset means unlimited and passes.
func validateMaxLifetime(v *viper.Viper) error {
	if !v.IsSet(keyMaxLifetime) {
		return nil
	}
	d := v.GetDuration(keyMaxLifetime)
	if d <= 0 {
		if s, ok := v.Get(keyMaxLifetime).(string); ok {
			if parsed, ok := ParseHumanDuration(s); ok {
				d = parsed
			}
		}
	}
	if d <= 0 {
		return fmt.Errorf("launcher config %s must be > 0, got %v", keyMaxLifetime, d)
	}
	if d%time.Second != 0 {
		return fmt.Errorf("launcher config %s must be a whole number of seconds, got %v", keyMaxLifetime, d)
	}
	return nil
}

// validateReadyTimeouts rejects a negative readiness timeout for the in-VM
// Docker engine or the agent serve daemon. Zero is the sentinel for the
// built-in default and is allowed; a negative value would make the readiness
// deadline expire at once, failing every VM start.
func validateReadyTimeouts(v *viper.Viper) error {
	for _, key := range []string{keyDockerReadyTimeout, keyDaemonReadyTimeout} {
		if d := v.GetDuration(key); d < 0 {
			return fmt.Errorf("launcher config %s must be >= 0, got %v", key, d)
		}
	}
	return nil
}
