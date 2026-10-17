package vm

import (
	"github.com/inoio/agents-sandbox/internal/sandbox/state"
)

func persistEnvSecrets(k state.Key, envState state.EnvState, secretState state.SecretState) error {
	return state.UpdateHomeState(k, func(st *state.HomeState) {
		st.EnvState = envState
		st.SecretState = secretState
	})
}

func persistNetworkState(k state.Key, networkState state.NetworkState) error {
	return state.UpdateHomeState(k, func(st *state.HomeState) {
		st.NetworkState = networkState
	})
}

func persistMountState(k state.Key, mountState state.MountState) error {
	return state.UpdateHomeState(k, func(st *state.HomeState) {
		st.MountState = mountState
	})
}
