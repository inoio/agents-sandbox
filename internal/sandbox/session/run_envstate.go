package session

import (
	"errors"
	"fmt"

	"gitlab.inoio.de/inoio/opencode-sandbox/internal/sandbox/state"
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
