package state

// ApprovedProjectLayer is the project config layer a checkout's user approved:
// the digest of its projection and the projection itself, kept so a later
// change can be shown as old → new. It holds no value from the project secret
// files (see specs/configuration/project-config-trust.md).
//
//exhaustruct:ignore
type ApprovedProjectLayer struct {
	Digest string         `yaml:"digest"`
	Inputs []TrustedInput `yaml:"inputs,omitempty"`
}

// TrustedInput is one approved project input: its source path, its kind, and
// its effect entries.
//
//exhaustruct:ignore
type TrustedInput struct {
	Path    string   `yaml:"path"`
	Kind    string   `yaml:"kind"`
	Entries []string `yaml:"entries,omitempty"`
}

// ApprovedProjectLayer returns the approval recorded for a checkout.
func (s *RootState) ApprovedProjectLayer(checkoutSlug string) (ApprovedProjectLayer, bool) {
	approval, ok := s.ProjectTrust[checkoutSlug]
	return approval, ok
}

// ApproveProjectLayer records a checkout's approval, replacing any earlier one.
func (s *RootState) ApproveProjectLayer(checkoutSlug string, approval ApprovedProjectLayer) {
	if s.ProjectTrust == nil {
		s.ProjectTrust = map[string]ApprovedProjectLayer{}
	}
	s.ProjectTrust[checkoutSlug] = approval
}
