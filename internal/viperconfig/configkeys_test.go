package viperconfig

import (
	"reflect"
	"slices"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"

	"github.com/spf13/pflag"
)

// The launcher's env-var and CLI-flag key sets are derived from the Config
// struct tags (#360). This golden list is the user-visible surface: a key
// disappearing from (or appearing in) a derived list changes which env vars and
// flags the launcher honors, so it must fail here first.
func TestConfigEnvKeysGoldenSet(t *testing.T) {
	want := []string{
		keyAutoPruneAge, keyManualPruneAge,
		"memory", "tmp-size", "disk-size", "workspace-quota",
		keyYes, "log-level", keyQuiet,
		keyAgent, KeyDocker, keyCPUs,
		keyProvisionHostConfig, KeyOpenLinks,
		keyAutoStopOnActiveSessions, keyAutoStopTimeout, keyAutoStopMaxSessionRetries,
		keyNetworkProfile, keyNetworkDNSServers,
		keyUpgradeMode, keyUpgradeInterval, keyUpgradeChannel,
		KeyDind,
	}
	got := slices.Clone(configEnvKeys)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("configEnvKeys = %v, want %v", got, want)
	}
}

// configBindKeys is offered to bindConfigFlags, which skips keys with no
// matching flag. It must cover every config-backed flag and must not contain
// the deprecated dind alias: binding --dind to viper would make
// IsSet("dind") always true, turning the dind precedence check into a spurious
// deprecation warning (see resolveDockerAlias).
func TestConfigBindKeysCoverTheConfigBackedFlags(t *testing.T) {
	if slices.Contains(configBindKeys, KeyDind) {
		t.Error("configBindKeys must not contain the deprecated dind alias")
	}
	for _, key := range []string{
		keyCPUs, "memory", "tmp-size", "disk-size", "workspace-quota",
		keyYes, keyQuiet, "log-level", keyAgent, KeyDocker, KeyOpenLinks,
	} {
		if !slices.Contains(configBindKeys, key) {
			t.Errorf("configBindKeys is missing the config-backed flag key %q", key)
		}
	}
}

// network.egress-allow/egress-deny are documented as config-file-only
// (docs/configuration/networking.md). The env:"-" tag on network.Policy keeps
// the derived env list from binding them; this asserts both the list and the
// end-to-end effect.
func TestConfigEgressListsAreConfigFileOnly(t *testing.T) {
	for _, key := range []string{"network.egress-allow", "network.egress-deny"} {
		if slices.Contains(configEnvKeys, key) {
			t.Errorf("configEnvKeys must not contain the config-file-only key %q", key)
		}
	}

	configpaths.WithMockConfigPaths(t)
	t.Setenv(envVarName(EnvPrefix, "network.egress-allow"), "host")

	r, err := NewResolver(nil, "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if len(r.cfg.Network.EgressAllow) != 0 {
		t.Errorf("Network.EgressAllow = %v, want none (its env var is not honored)", r.cfg.Network.EgressAllow)
	}
}

// provision-host-config is derived like every other key: its env var must be
// honored, and false must survive the on-by-default setting.
func TestResolverProvisionHostConfigEnv(t *testing.T) {
	configpaths.WithMockConfigPaths(t)
	t.Setenv("AGENTS_SANDBOX_PROVISION_HOST_CONFIG", "false")

	r, err := NewResolver(nil, "")
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.ProvisionHostConfig() {
		t.Error("ProvisionHostConfig = true, want AGENTS_SANDBOX_PROVISION_HOST_CONFIG=false to disable it")
	}
}

// The flag default type comes from the owning Config field's Go type, so a
// uint8 key needs no special case and a bool key cannot silently bind a string.
func TestFlagTypedDefaultUsesConfigFieldType(t *testing.T) {
	for _, tc := range []struct {
		key  string
		def  string
		want any
	}{
		{keyCPUs, "4", uint8(4)},
		{keyYes, "true", true},
		{KeyOpenLinks, "false", false},
		{KeyDocker, "true", true},
		{"memory", "4G", "4G"},
	} {
		if got := flagTypedDefault(tc.key, &pflag.Flag{DefValue: tc.def}); got != tc.want {
			t.Errorf("flagTypedDefault(%q, %q) = %#v, want %#v", tc.key, tc.def, got, tc.want)
		}
	}
}

// A field contributes a key only when it is exported *and* carries a
// mapstructure tag: an untagged exported field, the mapstructure "-" and
// env:"-" opt-outs, and an unexported field (tagged or not) are all skipped.
// Config has no untagged exported field and no tagged unexported field, so the
// synthetic type makes each guard observable on its own.
func TestWalkConfigKeysSkipsUntaggedAndUnexportedFields(t *testing.T) {
	type nested struct {
		Inner string `mapstructure:"inner"`
	}
	type sample struct {
		Tagged    string `mapstructure:"tagged"`
		Untagged  string
		Omitted   string `mapstructure:"-"`
		EnvAbsent string `mapstructure:"env-absent" env:"-"`
		Nested    nested `mapstructure:"nested"`

		//nolint:unused // read through the walk's reflection, not by name
		hidden string `mapstructure:"hidden"`
	}

	got := configKeyNames(walkConfigKeys(reflect.TypeFor[sample](), ""))

	want := []string{"tagged", "nested.inner"}
	if !slices.Equal(got, want) {
		t.Errorf("walkConfigKeys = %v, want %v", got, want)
	}
}
