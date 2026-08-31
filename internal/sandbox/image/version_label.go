package image

// OpenCodeVersionLabel is the Docker label carrying the opencode version baked
// into the image at build time. It is inherited by images built FROM the base
// and is read from the msb image cache to detect available upgrades.
const OpenCodeVersionLabel = "org.opencode-sandbox.opencode-version"

// parseImageVersion extracts the version from an OCI config's labels under the
// given version label, returning "" when the label is absent (pre-feature image).
func parseImageVersion(labels map[string]string, versionLabel string) string {
	return labels[versionLabel]
}
