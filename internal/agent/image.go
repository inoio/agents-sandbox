package agent

// ImageSpec carries the structured bits needed to bake an agent into the runner
// image: the version build ARG, the Docker label recording the baked version,
// an optional env var that disables runtime auto-update, and the install command.
type ImageSpec struct {
	VersionArg       string
	VersionLabel     string
	DisableUpdateEnv string
	InstallCommand   string
}
