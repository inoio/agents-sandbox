package prepare

import (
	"fmt"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/options"
	"github.com/inoio/agents-sandbox/internal/sandbox/resourceproposal"
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
	"github.com/inoio/agents-sandbox/internal/sandbox/volume"
	"github.com/inoio/agents-sandbox/internal/viperconfig"
)

// homeVolumePolicy is the configured answer to the home-volume question on an
// image change, remembering a dialog answer through rememberHomeVolumeAnswer. A
// skip-VM (--dry-run-vm) run remembers nothing: it must not persist a change.
func homeVolumePolicy(
	paths configpaths.ConfigPaths, opts options.RunOptions, k state.Key, skipVM bool,
) volume.ImageChangePolicy {
	policy := volume.ImageChangePolicy{Answer: opts.HomeVolumeOnImageChange, Remember: nil}
	if !skipVM {
		policy.Remember = func(answer string) (string, error) {
			return rememberHomeVolumeAnswer(paths, opts, k, answer)
		}
	}
	return policy
}

// rememberHomeVolumeAnswer writes answer as home-volume.on-image-change into
// the user config layer that sets ask, so the write takes effect. The answer is
// a personal preference, so a project config (shared with the team) that sets
// the key is never rewritten, and an environment variable that would override
// the write refuses it.
func rememberHomeVolumeAnswer(
	paths configpaths.ConfigPaths, opts options.RunOptions, k state.Key, answer string,
) (string, error) {
	key := viperconfig.KeyHomeVolumeOnImageChange
	slug := opts.ConfigSlug
	if slug == "" {
		slug = k.Slug
	}
	if owner, owned := viperconfig.LayerSettingOwner(paths, slug, key); owned && owner == paths.ProjectConfigDir() {
		return "", fmt.Errorf("%s is set in the project config (%s); change it there", key, owner)
	}
	return resourceproposal.PersistSetting(paths, slug, k.Slug, key, answer, false)
}
