package vm

import (
	"errors"
	"fmt"

	"github.com/inoio/opencode-sandbox/internal/sandbox/network"
	"github.com/inoio/opencode-sandbox/internal/sandbox/reprovision"
	"github.com/inoio/opencode-sandbox/internal/sandbox/state"
)

func persistEnvSecrets(slug string, envState state.EnvState, secretState state.SecretState) error {
	st, err := state.ReadState(slug)
	if err != nil {
		if errors.Is(err, state.ErrStateNotFound) {
			st = new(state.HomeState)
		} else {
			return fmt.Errorf("read state for persistence: %w", err)
		}
	}
	st.EnvState = envState
	st.SecretState = secretState
	return state.WriteState(slug, *st)
}

func persistNetworkState(slug string, policy network.Policy) error {
	st, err := state.ReadState(slug)
	if err != nil {
		if errors.Is(err, state.ErrStateNotFound) {
			st = new(state.HomeState)
		} else {
			return fmt.Errorf("read state for network persistence: %w", err)
		}
	}
	st.NetworkState = reprovision.BuildNetworkState(policy)
	return state.WriteState(slug, *st)
}
