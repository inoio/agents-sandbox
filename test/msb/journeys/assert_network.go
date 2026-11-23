package journeys

import (
	"fmt"
	"os"
	"strings"

	"github.com/inoio/agents-sandbox/internal/configdecode"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
	"github.com/inoio/agents-sandbox/internal/viperconfig"
)

// runNetworkPolicy is the policy a StepLaunch run under this journey already had:
// the profile its spec passes to `--network`, parsed. The journey literal is built
// before a Context exists, so StepAssertNetworkNotWidened takes this as a builder
// and derives the baseline when the step runs. The spec's profile is a validated
// name, so the parse cannot fail; an empty policy (rather than a panic) makes a
// hypothetical failure strict rather than silently lenient.
func runNetworkPolicy(c *Context) network.Policy {
	profiles, err := network.ParseProfiles([]string{c.Spec.NetworkOrDefault()})
	if err != nil {
		return network.Policy{}
	}
	//exhaustruct:ignore // only the profile is the baseline; every other policy field is zero
	return network.Policy{Profiles: profiles}
}

// StepAssertNetworkNotWidened asserts the scope's launcher config names no
// network profile or egress-allow host the run did not already have, so a
// learning session narrowed or kept the posture instead of widening it. baseline
// derives the run's pre-overlay policy from the context at run time, because the
// journey literal predates the Context; runNetworkPolicy is the builder for a run
// StepLaunch started. allowedEgress names the destinations the journey drove: the
// analyser's lock unions the current allow set with the hosts the session used
// (analyser.SessionFacts.networkFinding), so the journey's own destination must
// not read as a widening.
func StepAssertNetworkNotWidened(baseline func(*Context) network.Policy, allowedEgress ...string) Step {
	return AsCheck(Step{Name: "the learned network policy is no wider than the run's", Run: func(c *Context) error {
		dir, err := journeyScopeDir(c)
		if err != nil {
			return err
		}
		learned, err := scopeNetworkPosture(dir)
		if err != nil {
			return err
		}
		after := baseline(c)
		if widenedBeyond(learned, after, allowedEgress) {
			return fmt.Errorf(
				"the learned scope named a network posture beyond profiles %v / egress-allow %v: profiles %v, egress-allow %v",
				after.Profiles,
				after.EgressAllow,
				learned.profiles,
				learned.egressAllow,
			)
		}
		return nil
	}})
}

// networkPosture is the network policy a scope names: the profile(s) and the
// egress-allow list, the two keys the learn-mode lock writes.
type networkPosture struct {
	profiles    []network.Profile
	egressAllow []string
}

// scopeNetworkPosture reads the network posture the scope's launcher config
// names, or the zero posture when the scope wrote no config or no network block.
func scopeNetworkPosture(dir string) (networkPosture, error) {
	path, _, found, err := configdecode.FindFile(dir)
	if err != nil {
		return networkPosture{}, err
	}
	if !found {
		return networkPosture{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return networkPosture{}, fmt.Errorf("read %s: %w", path, err)
	}
	settings, err := viperconfig.ParseLayerSettings(path, data)
	if err != nil {
		return networkPosture{}, fmt.Errorf("parse %s: %w", path, err)
	}
	raw, _ := settings["network"].(map[string]any)
	if raw == nil {
		return networkPosture{}, nil
	}
	profiles, err := profilesFromSetting(raw["profile"])
	if err != nil {
		return networkPosture{}, err
	}
	egressAllow, err := stringsFromSetting(raw["egress-allow"])
	if err != nil {
		return networkPosture{}, err
	}
	return networkPosture{profiles: profiles, egressAllow: egressAllow}, nil
}

// profilesFromSetting decodes a network.profile setting, which the schema
// accepts as a single name, a comma-separated string, or a list.
func profilesFromSetting(value any) ([]network.Profile, error) {
	switch typed := value.(type) {
	case nil:
		return nil, nil
	case string:
		return network.ParseProfiles([]string{typed})
	case []string:
		return network.ParseProfiles(typed)
	case []any:
		values := make([]string, 0, len(typed))
		for _, entry := range typed {
			name, ok := entry.(string)
			if !ok {
				return nil, fmt.Errorf("network.profile entry %v is not a string", entry)
			}
			values = append(values, name)
		}
		return network.ParseProfiles(values)
	default:
		return nil, fmt.Errorf("network.profile has unexpected type %T", value)
	}
}

// stringsFromSetting decodes a config-file string-list setting the schema
// accepts as a single string or a list of strings, such as network.egress-allow.
func stringsFromSetting(value any) ([]string, error) {
	switch typed := value.(type) {
	case nil:
		return nil, nil
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil, nil
		}
		return []string{strings.TrimSpace(typed)}, nil
	case []string:
		return typed, nil
	case []any:
		values := make([]string, 0, len(typed))
		for _, entry := range typed {
			name, ok := entry.(string)
			if !ok {
				return nil, fmt.Errorf("network.egress-allow entry %v is not a string", entry)
			}
			values = append(values, name)
		}
		return values, nil
	default:
		return nil, fmt.Errorf("network.egress-allow has unexpected type %T", value)
	}
}

// widenedBeyond reports whether learned names a profile or egress-allow entry
// the baseline did not already allow (beyond allowedEgress, the destinations the
// journey drove), so a learned scope that kept or narrowed the posture passes.
//
// The terminal none profile is a narrowing whatever the baseline: network.
// checkProfileCombination makes it exclusive, so replacing any profile with none
// can only shrink egress. That is exactly what the analyser writes when it
// narrows a broad profile (analyser.ConfigFacts.broadProfileFinding), so the
// attribution journey's narrowing must pass here even when the run started on
// public/private.
//
// The egress-allow list is the lock's other half: under a profile that admitted
// broad egress the analyser writes none plus the destinations the session used
// (analyser.SessionFacts.networkFinding unions the current allow set with the
// observed-used hosts), so the guard admits entries the baseline allowed or the
// journey drove, and flags any other host.
func widenedBeyond(learned networkPosture, baseline network.Policy, allowedEgress []string) bool {
	allowedProfiles := make(map[network.Profile]bool, len(baseline.Profiles))
	for _, profile := range baseline.Profiles {
		allowedProfiles[profile] = true
	}
	for _, profile := range learned.profiles {
		if profile == network.ProfileNone {
			continue
		}
		if !allowedProfiles[profile] {
			return true
		}
	}

	allowedHosts := make(map[string]bool)
	for _, host := range baseline.EgressAllow {
		allowedHosts[host] = true
	}
	for _, host := range allowedEgress {
		allowedHosts[host] = true
	}
	for _, host := range learned.egressAllow {
		if !allowedHosts[host] {
			return true
		}
	}
	return false
}
