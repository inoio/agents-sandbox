package viperconfig

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/homeconfig"
	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/yamlfmt"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/titanous/json5"
)

// ConfigLayers returns the launcher-config layers from lowest to highest
// precedence, matching NewResolver's merge order and its documented precedence
// (generic user dir, per-slug user dir when slug is non-empty, project dir,
// legacy env, env, flags). NewResolver merges the file layers from this slice,
// so the order shown to the user and the order actually merged cannot drift.
func ConfigLayers(slug string) []ConfigLayer {
	cfg := configpaths.Get()
	layers := []ConfigLayer{
		fileLayer(LayerNameGenericUserConfig, cfg.UserConfigDir()),
	}
	if slug != "" {
		layers = append(layers, fileLayer(LayerNamePerSlugUserConfig, cfg.UserSlugConfigDir(slug)))
	}
	layers = append(layers,
		fileLayer(LayerNameProjectConfig, cfg.ProjectConfigDir()),
		ConfigLayer{Name: LayerNameLegacyEnv, Path: "", Present: false},
		ConfigLayer{Name: LayerNameEnv, Path: "", Present: false},
		ConfigLayer{Name: LayerNameFlags, Path: "", Present: false},
	)
	return layers
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
func NewResolver(cmd *cobra.Command, slug string) (*Resolver, error) {
	v := viper.New()

	for _, layer := range ConfigLayers(slug) {
		// mergeDir with an empty path is a no-op, so the env and flag
		// layers need no guard here.
		if err := mergeDir(v, layer.Path); err != nil {
			return nil, err
		}
	}

	v.SetEnvPrefix(EnvPrefix)
	// Env-var keys are all top-level and dash-separated; only the nested
	// "network.profile" key uses a dot, which maps to "_" here. This is a
	// repo-wide replacement, so a future dotted top-level key (e.g.
	// "auto.stop") would collide with a dashed one ("auto-stop") and must be
	// avoided.
	v.SetEnvKeyReplacer(envKeyReplacer)
	v.AutomaticEnv()
	for _, key := range configEnvKeys {
		// Keys are non-empty constants, so binding cannot fail.
		v.MustBindEnv(key, envVarName(LegacyEnvPrefix, key))
	}

	if cmd != nil {
		bindConfigFlags(v, cmd)
	}

	// Non-flag defaults: host config provisioning is off unless opted in.
	v.SetDefault(keyProvisionHostConfig, false)

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

	homeLayers, hasHome, err := homeconfig.LoadLayers([]string{
		configpaths.Get().UserConfigDir(),
		configpaths.Get().ProjectConfigDir(),
	})
	if err != nil {
		return nil, fmt.Errorf("decode launcher config: %w", err)
	}
	cfg.Home = homeLayers
	cfg.hasHome = hasHome
	docker, dockerSource, dockerLegacy := resolveDockerAlias(v, cmd, cfg.Docker)
	cfg.Docker = docker
	cfg.dockerSource = dockerSource
	higherScope, err := higherScopeKeys(cmd, slug)
	if err != nil {
		return nil, err
	}
	return &Resolver{
		cfg:          cfg,
		legacyEnv:    legacyEnvVarsInUse(),
		dockerLegacy: dockerLegacy,
		higherScope:  higherScope,
	}, nil
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
	return &Resolver{cfg: cfg, legacyEnv: nil, dockerLegacy: "", higherScope: map[string]bool{}}
}

func mergeDir(v *viper.Viper, dir string) error {
	path, ext, ok := configpaths.FindConfigFile(dir)
	if !ok {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read launcher config %s: %w", path, err)
	}
	ct := configType(ext)
	if ext == configpaths.ExtJSON5 || ext == configpaths.ExtJSONC {
		var m map[string]any
		if unmarshalErr := json5.Unmarshal(data, &m); unmarshalErr != nil {
			return fmt.Errorf("parse launcher config %s: %w", path, unmarshalErr)
		}
		data, err = json.Marshal(m)
		if err != nil {
			return fmt.Errorf("normalize launcher config %s: %w", path, err)
		}
		// configType already reports both JSON5 and JSONC as JSON.
	}
	v.SetConfigType(ct)
	if err := v.MergeConfig(bytes.NewReader(data)); err != nil {
		if ct == ctYAML {
			return yamlfmt.WrapErr(path, err)
		}
		return fmt.Errorf("load launcher config %s: %w", path, err)
	}
	return nil
}

func configType(ext string) string {
	switch strings.ToLower(ext) {
	case ".yaml", ".yml":
		return ctYAML
	case ".json", configpaths.ExtJSONC, configpaths.ExtJSON5:
		return ctJSON
	}
	return ""
}
