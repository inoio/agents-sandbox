package vm

import (
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
)

func persistEnvSecrets(k state.Key, envState state.EnvState, secretState state.SecretState) error {
	return state.WithHomeState(k, func(st *state.HomeState, _ bool) (bool, error) {
		st.EnvState = envState
		st.SecretState = secretState
		return true, nil
	})
}

func persistNetworkState(k state.Key, networkState state.NetworkState) error {
	return state.WithHomeState(k, func(st *state.HomeState, _ bool) (bool, error) {
		st.NetworkState = networkState
		return true, nil
	})
}

func persistMountState(k state.Key, mountState state.MountState) error {
	return state.WithHomeState(k, func(st *state.HomeState, _ bool) (bool, error) {
		st.MountState = mountState
		return true, nil
	})
}
