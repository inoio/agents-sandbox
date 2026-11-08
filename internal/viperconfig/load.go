package viperconfig

import (
	"fmt"
	"os"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/inoio/agents-sandbox/internal/configdecode"
	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/homeconfig"
	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
)

// ConfigLayers returns the launcher-config layers from lowest to highest
// precedence, matching NewResolver's merge order and its documented precedence
// (generic user dir, per-slug user dir when slug is non-empty, project dir,
// legacy env, env, flags). The file layers are a projection of the config
// scopes, so the order shown to the user and the order actually merged cannot
// drift. NewResolver merges the file layers from this slice.
func ConfigLayers(paths configpaths.ConfigPaths, slug string) []ConfigLayer {
	scopes := configpaths.ConfigScopes(paths, slug)
	var layers []ConfigLayer
	for _, scope := range scopes {
		layers = append(layers, fileLayer(layerNameForScope(scope.Kind), scope.Dir))
	}
	layers = append(layers,
		ConfigLayer{Name: LayerNameLegacyEnv, Path: "", Present: false},
		ConfigLayer{Name: LayerNameEnv, Path: "", Present: false},
		ConfigLayer{Name: LayerNameFlags, Path: "", Present: false},
	)
	return layers
}

// scopeLayerNames maps each config scope kind to its config-file layer name.
// The map is exhaustive over ScopeKind, so a lookup cannot produce an unknown
// name for a kind ConfigScopes emits.
var scopeLayerNames = map[configpaths.ScopeKind]string{
	configpaths.ScopeGenericUser: LayerNameGenericUserConfig,
	configpaths.ScopePerSlugUser: LayerNamePerSlugUserConfig,
	configpaths.ScopeProject:     LayerNameProjectConfig,
}

// layerNameForScope maps a config scope kind to its config-file layer name.
func layerNameForScope(kind configpaths.ScopeKind) string {
	return scopeLayerNames[kind]
}

// fileLayer reports the directory of a config-file layer and whether a config
// file exists there.
func fileLayer(name, dir string) ConfigLayer {
	_, _, present := configpaths.FindConfigFile(dir)
	return ConfigLayer{Name: name, Path: dir, Present: present}
}

// NewResolver builds a Resolver, loading config files, configuring the
// AGENTS_SANDBOX_ env prefix (with the deprecated OPENCODE_SANDBOX_ aliases as
// a lower-precedence fallback), binding config-backed flags on cmd, and
// validating. Config precedence (lowest to highest): generic user dir,
// per-slug user dir (when slug is non-empty), project dir, legacy env, env,
// flags. The file layers and their order come from ConfigLayers so the merged
// order matches the order ConfigLayers reports. cmd may be nil to skip flag
// binding.
func NewResolver(cmd *cobra.Command, paths configpaths.ConfigPaths, slug string) (*Resolver, error) {
	v := viper.New()

	for _, layer := range ConfigLayers(paths, slug) {
		// mergeDir with an empty path is a no-op, so the env and flag
		// layers need no guard here.
		if err := mergeDir(v, layer.Path); err != nil {
			return nil, err
		}
	}

	// Env vars are bound explicitly rather than through viper's AutomaticEnv:
	// AutomaticEnv resolves *any* key from the environment, so an
	// AGENTS_SANDBOX_<KEY> variable would override a config-file-only key
	// (env:"-") whenever a config file sets it. Binding only envKeys keeps
	// those keys config-file-only. Each key binds the canonical
	// AGENTS_SANDBOX_ name first and the deprecated OPENCODE_SANDBOX_ alias
	// second, so the canonical value wins when both are set.
	for _, key := range envKeys {
		// Keys are non-empty constants, so binding cannot fail.
		v.MustBindEnv(key, envVarName(EnvPrefix, key), envVarName(LegacyEnvPrefix, key))
	}

	if cmd != nil {
		bindConfigFlags(v, cmd)
	}

	// Non-flag defaults: host config provisioning is on unless opted out.
	v.SetDefault(keyProvisionHostConfig, true)

	if err := validate(v); err != nil {
		return nil, err
	}

	var cfg Config
	if err := v.Unmarshal(&cfg, viper.DecodeHook(
		mapstructure.ComposeDecodeHookFunc(
			durationDecodeHook(),
			mapstructure.StringToTimeDurationHookFunc(),
			// Split comma-separated strings (env vars, CLI StringSlice flags)
			// into []string fields such as network.egress-allow and
			// network.dns-servers, and into []Profile for network.profile. It
			// also leniently accepts a scalar string for those list fields in
			// config files.
			stringToSliceDecodeHook(),
		),
	)); err != nil {
		return nil, fmt.Errorf("decode launcher config: %w", err)
	}
	decodedMounts, err := mounts.DecodeMounts(v.Get("mounts"))
	if err != nil {
		return nil, fmt.Errorf("decode launcher config: %w", err)
	}
	cfg.Mounts = decodedMounts
	cfg.Notify = decodeNotify(v)

	homeLayers, hasHome, err := homeconfig.LoadLayers(
		configpaths.ScopeDirs(configpaths.ConfigScopes(paths, slug)),
	)
	if err != nil {
		return nil, fmt.Errorf("decode launcher config: %w", err)
	}
	cfg.Home = homeLayers
	cfg.hasHome = hasHome
	cfg.ConfigSlug = slug
	docker, dockerSource, dockerLegacy := resolveDockerAlias(v, cmd, cfg.Docker)
	cfg.Docker = docker
	cfg.dockerSource = dockerSource
	return &Resolver{cfg: cfg, slug: slug, legacyEnv: legacyEnvVarsInUse(), dockerLegacy: dockerLegacy}, nil
}

// resolveDockerAlias folds the deprecated dind alias into the docker setting
// through EffectiveDockerSource, the single docker/dind precedence. The
// canonical docker value wins whenever a canonical source is present (config
// key, env, or the --docker flag); otherwise a deprecated dind value (config
// key or AGENTS_SANDBOX_DIND) is used. It returns the effective value, the
// source it came from, and the deprecated source name to warn about (empty when
// the canonical setting won).
//
// With no source at all the winner is the zero DockerSourceValue — docker off
// with DockerSourceNone — and that equals the unmarshaled value: a true docker
// value always carries a canonical source, since viper's docker default is the
// --docker flag's false default.
//
// The --dind flag is not among the sources here: it is not bound to viper, so
// the run and build paths fold it in over this resolver with the same
// precedence.
func resolveDockerAlias(v *viper.Viper, cmd *cobra.Command, value bool) (bool, DockerSource, string) {
	winner := EffectiveDockerSource(dockerSources(v, cmd, value)...)
	legacy := ""
	if winner.Source.Deprecated() {
		legacy = dockerLegacySource(v)
	}
	return winner.Value, winner.Source, legacy
}

// dockerSources lists the docker and deprecated dind sources the config and env
// layers carry, each with the value it contributes. The docker value is the
// merged one viper resolved with flag > env > config.
func dockerSources(v *viper.Viper, cmd *cobra.Command, value bool) []DockerSourceValue {
	var sources []DockerSourceValue
	if source := canonicalDockerSource(v, cmd); source != DockerSourceNone {
		sources = append(sources, DockerSourceValue{Source: source, Value: value})
	}
	if v.IsSet(KeyDind) {
		sources = append(sources, DockerSourceValue{Source: dindSource(), Value: v.GetBool(KeyDind)})
	}
	return sources
}

// canonicalDockerSource reports the highest-precedence canonical docker source
// present. The deprecated OPENCODE_SANDBOX_ prefix counts too: NewResolver still
// reads it, so it must also be surfaced as explicit rather than ignored by the
// project-Dockerfile decision.
func canonicalDockerSource(v *viper.Viper, cmd *cobra.Command) DockerSource {
	switch {
	case dockerFlagChanged(cmd):
		return DockerSourceDockerFlag
	case os.Getenv(envVarName(EnvPrefix, KeyDocker)) != "":
		return DockerSourceDockerEnv
	case os.Getenv(envVarName(LegacyEnvPrefix, KeyDocker)) != "":
		return DockerSourceDockerEnv
	case v.InConfig(KeyDocker):
		return DockerSourceDockerConfig
	}
	// gomutants:disable-next-line RETURN_ZERO reason="DockerSourceNone is the DockerSource zero value, so the mutant is the identical constant"
	return DockerSourceNone
}

// dockerFlagChanged reports whether a canonical --docker flag was explicitly set
// on cmd, which viper cannot see because it binds the flag's value only.
func dockerFlagChanged(cmd *cobra.Command) bool {
	if cmd == nil {
		return false
	}
	flag := findFlag(cmd, KeyDocker)
	return flag != nil && flag.Changed
}

// dindSource names the deprecated dind source IsSet reports, preferring the
// environment variable over the config key (the layer order viper resolves).
func dindSource() DockerSource {
	if os.Getenv(envVarName(EnvPrefix, KeyDind)) != "" ||
		os.Getenv(envVarName(LegacyEnvPrefix, KeyDind)) != "" {
		return DockerSourceDindEnv
	}
	return DockerSourceDindConfig
}

// dockerLegacySource names where the deprecated dind value came from.
func dockerLegacySource(v *viper.Viper) string {
	if env := envVarName(EnvPrefix, KeyDind); os.Getenv(env) != "" {
		return env
	}
	if v.InConfig(KeyDind) {
		return "the dind config key"
	}
	return "dind"
}

// NewResolverWithConfig builds a Resolver from an explicit Config. It is
// used by callers (notably cmd tests) that need a resolver with known values
// without touching config files or env.
//
//deadcode:ignore reason="construction seam for cmd tests; see doc comment"
func NewResolverWithConfig(cfg Config) *Resolver {
	return &Resolver{cfg: cfg, slug: "", legacyEnv: nil, dockerLegacy: ""}
}

// ParseLayerSettings parses the launcher config file data read from path like
// a merged layer, and returns the settings it sets (keys lowercased, nested
// maps kept).
func ParseLayerSettings(path string, data []byte) (map[string]any, error) {
	values, err := configdecode.DecodeData(path, "launcher config", data)
	if err != nil {
		return nil, err
	}
	v := viper.New()
	// MergeConfigMap never fails; merging normalizes the keys as a layer would.
	_ = v.MergeConfigMap(values)
	return v.AllSettings(), nil
}

func mergeDir(v *viper.Viper, dir string) error {
	path, _, ok := configpaths.FindConfigFile(dir)
	if !ok {
		return nil
	}
	values, err := configdecode.DecodeFile(path, "launcher config")
	if err != nil {
		return err
	}
	return v.MergeConfigMap(values)
}
