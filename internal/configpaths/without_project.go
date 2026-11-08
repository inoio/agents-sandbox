package configpaths

// WithoutProjectLayer returns paths with the project layer removed: the
// project config dir, its absolute form, the project Dockerfile, and the
// ignored worktree config dir are empty, and ConfigScopes leaves out the
// project scope. Every user path delegates to paths unchanged. It is how an
// unapproved project config is kept out of a run.
func WithoutProjectLayer(paths ConfigPaths) ConfigPaths {
	return withoutProjectLayer{ConfigPaths: paths}
}

type withoutProjectLayer struct {
	ConfigPaths
}

func (withoutProjectLayer) ProjectConfigDir() string         { return "" }
func (withoutProjectLayer) ProjectConfigDirAbs() string      { return "" }
func (withoutProjectLayer) ProjectDockerfile() string        { return "" }
func (withoutProjectLayer) IgnoredWorktreeConfigDir() string { return "" }

// WithProjectLayer undoes WithoutProjectLayer and returns any other paths
// unchanged.
func WithProjectLayer(paths ConfigPaths) ConfigPaths {
	if excluded, ok := paths.(withoutProjectLayer); ok {
		return excluded.ConfigPaths
	}
	return paths
}

// Use makes factory the process path factory Get and returns a function that
// restores the previous one.
func Use(factory func() ConfigPaths) func() {
	previous := Get
	Get = factory
	return func() { Get = previous }
}
