package viperconfig

import (
	"github.com/inoio/agents-sandbox/internal/notify"
)

// resolverNotify returns the resolver's notify config, normalizing an empty
// Audio to AudioOff just as the removed Resolver.Notify getter did.
func resolverNotify(r *Resolver) notify.Config {
	cfg := r.cfg.Notify
	if cfg.Audio == "" {
		cfg.Audio = notify.AudioOff
	}
	return cfg
}
