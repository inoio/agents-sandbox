package viperconfig

// DockerSource identifies the explicit source a docker-capability value came
// from. The constants are numbered by precedence, most significant first: every
// canonical docker source outranks every deprecated dind alias, and within one
// kind the higher layer wins (flag > env > config). That is the deprecation
// policy's "replacement wins" rule: a deprecated alias resolves only when no
// canonical source is set.
type DockerSource uint8

const (
	// DockerSourceNone means no explicit docker source is present.
	DockerSourceNone DockerSource = iota
	// DockerSourceDockerFlag is the --docker flag.
	DockerSourceDockerFlag
	// DockerSourceDockerEnv is AGENTS_SANDBOX_DOCKER, or the deprecated
	// OPENCODE_SANDBOX_DOCKER prefix.
	DockerSourceDockerEnv
	// DockerSourceDockerConfig is the docker config key.
	DockerSourceDockerConfig
	// DockerSourceDindFlag is the deprecated --dind flag.
	DockerSourceDindFlag
	// DockerSourceDindEnv is the deprecated AGENTS_SANDBOX_DIND variable, or
	// the deprecated OPENCODE_SANDBOX_DIND prefix.
	DockerSourceDindEnv
	// DockerSourceDindConfig is the deprecated dind config key.
	DockerSourceDindConfig
)

// Deprecated reports whether the source is a deprecated dind alias rather than
// the canonical docker setting, so a caller can warn about the spelling in use.
// It reads the enum's ordering: the deprecated aliases are the tail of the list.
func (s DockerSource) Deprecated() bool {
	return s >= DockerSourceDindFlag
}

// DockerSourceValue is one present docker-capability source and the value it
// carries.
type DockerSourceValue struct {
	Source DockerSource
	Value  bool
}

// EffectiveDockerSource returns the present source with the highest precedence,
// or the zero DockerSourceValue when no source is present. A source counts as
// present as soon as it is passed, a present-but-false one included, so an
// explicit "off" suppresses every lower-precedence source instead of being
// re-enabled by it. Sources are examined in order, so the first of several with
// equal precedence wins.
func EffectiveDockerSource(sources ...DockerSourceValue) DockerSourceValue {
	var winner DockerSourceValue
	for _, source := range sources {
		if source.Source == DockerSourceNone {
			continue
		}
		if winner.Source == DockerSourceNone || source.Source < winner.Source {
			winner = source
		}
	}
	return winner
}
