package vm

import (
	"context"

	"github.com/inoio/agents-sandbox/internal/sandbox/envsecret"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
)

// lookUpAndEnsureProjectVM looks the project VM up through the request's msb
// client and ensures it with empty host env and secrets, as the plan-reconfig
// and ensure-vm stages do together.
// A skipVM run leaves the VM alone, so it needs no lookup and no msb mock.
func lookUpAndEnsureProjectVM(ctx context.Context, creation ProjectVMCreation) (msb.Sandbox, VMBoot, error) {
	if !creation.SkipVM {
		existing, _, err := FindProjectVM(ctx, creation.Client, creation.Key)
		if err != nil {
			return nil, VMBootConnected, err
		}
		creation.Existing = existing
	}
	creation.Env = envsecret.EnvSecrets{}
	return EnsureProjectVM(ctx, creation)
}
