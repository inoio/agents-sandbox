package session

import (
	"errors"
	"fmt"

	"gitlab.inoio.de/inoio/opencode-msb/internal/sandbox/state"
	"gitlab.inoio.de/inoio/opencode-msb/internal/termio"
)

func currentEnvState(slug string, ui termio.UI) state.EnvState {
	st, err := state.ReadState(slug)
	if err != nil {
		if !errors.Is(err, state.ErrStateNotFound) {
			ui.Warnf("reading state for env fingerprint: %v (continuing)", err)
		}
		return state.EnvState{}
	}
	return st.EnvState
}

func currentSecretState(slug string, ui termio.UI) state.SecretState {
	st, err := state.ReadState(slug)
	if err != nil {
		if !errors.Is(err, state.ErrStateNotFound) {
			ui.Warnf("reading state for secret fingerprint: %v (continuing)", err)
		}
		return state.SecretState{}
	}
	return st.SecretState
}

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
