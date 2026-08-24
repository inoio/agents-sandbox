package reprovision

import (
	"testing"

	msbSdk "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/inoio/opencode-sandbox/internal/sandbox/network"
	"github.com/inoio/opencode-sandbox/internal/sandbox/state"
)

func TestEnvContentHashOrderIndependent(t *testing.T) {
	a := EnvContentHash(map[string]string{"B": "2", "A": "1"})
	b := EnvContentHash(map[string]string{"A": "1", "B": "2"})
	if a != b {
		t.Errorf("EnvContentHash not order-independent: %q vs %q", a, b)
	}
	if EnvContentHash(nil) != EnvContentHash(map[string]string{}) {
		t.Error("EnvContentHash(nil) should equal EnvContentHash(empty)")
	}
	if a == EnvContentHash(map[string]string{"A": "1", "B": "3"}) {
		t.Error("EnvContentHash should differ when a value changes")
	}
}

func TestSecretsContentHashStable(t *testing.T) {
	a := SecretsContentHash([]msbSdk.SecretEntry{{EnvVar: "A", Value: "1"}, {EnvVar: "B", Value: "2"}})
	b := SecretsContentHash([]msbSdk.SecretEntry{{EnvVar: "B", Value: "2"}, {EnvVar: "A", Value: "1"}})
	if a != b {
		t.Errorf("SecretsContentHash not order-independent: %q vs %q", a, b)
	}
	if SecretsContentHash(nil) != SecretsContentHash([]msbSdk.SecretEntry{}) {
		t.Error("SecretsContentHash(nil) should equal SecretsContentHash(empty)")
	}
}

func TestEnvChanged(t *testing.T) {
	hash := EnvContentHash(map[string]string{"A": "1"})
	if EnvChanged(state.EnvState{Hash: hash}, map[string]string{"A": "1"}) {
		t.Error("EnvChanged should be false when hashes match")
	}
	if !EnvChanged(state.EnvState{Hash: hash}, map[string]string{"A": "2"}) {
		t.Error("EnvChanged should be true when a value changes")
	}
	if !EnvChanged(state.EnvState{}, map[string]string{"A": "1"}) {
		t.Error("EnvChanged with empty applied and non-empty desired should be true")
	}
	if EnvChanged(state.EnvState{}, nil) {
		t.Error("EnvChanged with empty applied and empty desired should be false")
	}
}

func TestSecretsChanged(t *testing.T) {
	hash := SecretsContentHash([]msbSdk.SecretEntry{{EnvVar: "A", Value: "1"}})
	if SecretsChanged(state.SecretState{Hash: hash}, []msbSdk.SecretEntry{{EnvVar: "A", Value: "1"}}) {
		t.Error("SecretsChanged should be false when hashes match")
	}
	if !SecretsChanged(state.SecretState{Hash: hash}, []msbSdk.SecretEntry{{EnvVar: "A", Value: "2"}}) {
		t.Error("SecretsChanged should be true when a value changes")
	}
	if !SecretsChanged(state.SecretState{}, []msbSdk.SecretEntry{{EnvVar: "A", Value: "1"}}) {
		t.Error("SecretsChanged with empty applied and non-empty desired should be true")
	}
	if SecretsChanged(state.SecretState{}, nil) {
		t.Error("SecretsChanged with empty applied and nil desired should be false")
	}
}

func TestBuildEnvState(t *testing.T) {
	st := BuildEnvState(map[string]string{"B": "2", "A": "1"})
	if st.Hash != EnvContentHash(map[string]string{"A": "1", "B": "2"}) {
		t.Errorf("BuildEnvState.Hash mismatch")
	}
	if len(st.Names) != 2 || st.Names[0] != "A" || st.Names[1] != "B" {
		t.Errorf("BuildEnvState.Names = %v, want sorted [A B]", st.Names)
	}
}

func TestBuildSecretState(t *testing.T) {
	st := BuildSecretState([]msbSdk.SecretEntry{{EnvVar: "B", Value: "2"}, {EnvVar: "A", Value: "1"}})
	if st.Hash != SecretsContentHash([]msbSdk.SecretEntry{{EnvVar: "A", Value: "1"}, {EnvVar: "B", Value: "2"}}) {
		t.Errorf("BuildSecretState.Hash mismatch")
	}
	if len(st.Names) != 2 || st.Names[0] != "A" || st.Names[1] != "B" {
		t.Errorf("BuildSecretState.Names = %v, want sorted [A B]", st.Names)
	}
}

func TestNetworkChanged(t *testing.T) {
	policy := network.Policy{Profile: network.ProfilePublic}
	fp := policy.Fingerprint()
	if NetworkChanged(state.NetworkState{Hash: fp}, policy) {
		t.Error("NetworkChanged should be false when hashes match")
	}
	if !NetworkChanged(state.NetworkState{Hash: fp}, network.Policy{Profile: network.ProfileNone}) {
		t.Error("NetworkChanged should be true when profile changes")
	}
	if !NetworkChanged(state.NetworkState{}, policy) {
		t.Error("NetworkChanged with empty applied and non-empty desired should be true")
	}
	if NetworkChanged(state.NetworkState{}, network.Policy{}) {
		t.Error("NetworkChanged with empty applied and empty desired should be false")
	}
}

func TestBuildNetworkState(t *testing.T) {
	policy := network.Policy{Profile: network.ProfileNone}
	st := BuildNetworkState(policy)
	if st.Hash != policy.Fingerprint() {
		t.Errorf("BuildNetworkState.Hash = %q, want fingerprint %q", st.Hash, policy.Fingerprint())
	}
	if len(st.Names) != 1 || st.Names[0] != string(network.ProfileNone) {
		t.Errorf("BuildNetworkState.Names = %v, want [none]", st.Names)
	}
}
