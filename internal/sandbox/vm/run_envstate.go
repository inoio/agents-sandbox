package vm

import (
	"errors"
	"fmt"

	"github.com/inoio/opencode-sandbox/internal/sandbox/mounts"
	"github.com/inoio/opencode-sandbox/internal/sandbox/network"
	"github.com/inoio/opencode-sandbox/internal/sandbox/reprovision"
	"github.com/inoio/opencode-sandbox/internal/sandbox/state"
)

func persistEnvSecrets(k state.Key, envState state.EnvState, secretState state.SecretState) error {
	st, err := state.ReadState(k)
	if err != nil {
		if errors.Is(err, state.ErrStateNotFound) {
			st = new(state.HomeState)
		} else {
			return fmt.Errorf("read state for persistence: %w", err)
		}
	}
	st.EnvState = envState
	st.SecretState = secretState
	return state.WriteState(k, *st)
}

func persistNetworkState(k state.Key, policy network.Policy) error {
	st, err := state.ReadState(k)
	if err != nil {
		if errors.Is(err, state.ErrStateNotFound) {
			st = new(state.HomeState)
		} else {
			return fmt.Errorf("read state for network persistence: %w", err)
		}
	}
	st.NetworkState = reprovision.BuildNetworkState(policy)
	return state.WriteState(k, *st)
}

func persistMountState(k state.Key, mounts mounts.Mounts) error {
	st, err := state.ReadState(k)
	if err != nil {
		if errors.Is(err, state.ErrStateNotFound) {
			st = new(state.HomeState)
		} else {
			return fmt.Errorf("read state for mount persistence: %w", err)
		}
	}
	st.MountState = reprovision.BuildMountState(mounts)
	return state.WriteState(k, *st)
}
