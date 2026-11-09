package state

import (
	"slices"
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/sandbox/envsecret"
	"github.com/inoio/agents-sandbox/internal/sandbox/mounts"
	"github.com/inoio/agents-sandbox/internal/sandbox/msb"
	"github.com/inoio/agents-sandbox/internal/sandbox/network"
	"github.com/inoio/agents-sandbox/internal/termio"
	"github.com/inoio/agents-sandbox/internal/termio/termiotest"
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
	a := SecretsContentHash([]msb.SecretEntry{{EnvVar: "A", Value: "1"}, {EnvVar: "B", Value: "2"}})
	b := SecretsContentHash([]msb.SecretEntry{{EnvVar: "B", Value: "2"}, {EnvVar: "A", Value: "1"}})
	if a != b {
		t.Errorf("SecretsContentHash not order-independent: %q vs %q", a, b)
	}
	if SecretsContentHash(nil) != SecretsContentHash([]msb.SecretEntry{}) {
		t.Error("SecretsContentHash(nil) should equal SecretsContentHash(empty)")
	}
}

func TestEnvChanged(t *testing.T) {
	hash := EnvContentHash(map[string]string{"A": "1"})
	if EnvChanged(EnvState{Hash: hash}, map[string]string{"A": "1"}) {
		t.Error("EnvChanged should be false when hashes match")
	}
	if !EnvChanged(EnvState{Hash: hash}, map[string]string{"A": "2"}) {
		t.Error("EnvChanged should be true when a value changes")
	}
	if !EnvChanged(EnvState{}, map[string]string{"A": "1"}) {
		t.Error("EnvChanged with empty applied and non-empty desired should be true")
	}
	if EnvChanged(EnvState{}, nil) {
		t.Error("EnvChanged with empty applied and empty desired should be false")
	}
}

func TestSecretsContentHashIncludesHosts(t *testing.T) {
	// A change to a secret's allowed hosts (with the same value) must produce
	// a different hash so the launcher recreates the VM. See SecretsContentHash.
	a := SecretsContentHash([]msb.SecretEntry{
		{EnvVar: "GITHUB_TOKEN", Value: "t", Allow: []string{"github.com"}},
	})
	b := SecretsContentHash([]msb.SecretEntry{
		{EnvVar: "GITHUB_TOKEN", Value: "t", Allow: []string{"api.github.com", "github.com"}},
	})
	if a == b {
		t.Error("SecretsContentHash should differ when Allow changes with the same value")
	}
}

func TestSecretsContentHashIncludesOtherFields(t *testing.T) {
	base := msb.SecretEntry{EnvVar: "A", Value: "1"}
	baseHash := SecretsContentHash([]msb.SecretEntry{base})

	boolPtr := func(v bool) *bool { return &v }
	tests := []struct {
		name   string
		mutate func(e *msb.SecretEntry)
	}{
		{"Value", func(e *msb.SecretEntry) { e.Value = "2" }},
		{"Allow", func(e *msb.SecretEntry) { e.Allow = []string{"github.com"} }},
		{"Passthrough", func(e *msb.SecretEntry) { e.Passthrough = []string{"*.github.com"} }},
		{"Placeholder", func(e *msb.SecretEntry) { e.Placeholder = "$GITHUB_TOKEN" }},
		{"RequireTLSIdentity true", func(e *msb.SecretEntry) { e.RequireTLSIdentity = boolPtr(true) }},
		{"RequireTLSIdentity false", func(e *msb.SecretEntry) { e.RequireTLSIdentity = boolPtr(false) }},
		{"Substitution headers true", func(e *msb.SecretEntry) { e.Substitution.Headers = boolPtr(true) }},
		{"Substitution headers false", func(e *msb.SecretEntry) { e.Substitution.Headers = boolPtr(false) }},
		{"Substitution query", func(e *msb.SecretEntry) { e.Substitution.Query = true }},
		{"Substitution body", func(e *msb.SecretEntry) { e.Substitution.Body = true }},
		{
			"Substitution header fields",
			func(e *msb.SecretEntry) { e.Substitution.HeaderFields = []string{"authorization"} },
		},
		{"ViolationAction", func(e *msb.SecretEntry) { e.ViolationAction = msb.ViolationActionBlock }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := base
			tt.mutate(&e)
			if got := SecretsContentHash([]msb.SecretEntry{e}); got == baseHash {
				t.Errorf("SecretsContentHash should differ when %s changes", tt.name)
			}
		})
	}
}

// TestSecretsContentHashAllHeadersStable pins the digest of an all-headers
// secret (no header_fields) to the value the record format produced before
// header scoping existed. An unconditional change to the line would hash every
// existing secret differently and recreate every project VM once on upgrade,
// which this change must not do. Treat a mismatch here as deliberate: it means
// field scoping was folded into the fingerprint in a way that also moves the
// no-scope case.
func TestSecretsContentHashAllHeadersStable(t *testing.T) {
	const golden = "26f56b74321a6352bfaa9ef9c5434c1929f5e9b2c6416b9cf1f4803437ce3ce4"
	got := SecretsContentHash([]msb.SecretEntry{{EnvVar: "A", Value: "1"}})
	if got != golden {
		t.Errorf("all-headers secret hash = %q, want the pre-scoping value %q", got, golden)
	}
}

// TestSecretsContentHashCaseOrderInsensitiveAcrossSpecs pins the review-focus
// invariant end to end: two specs that differ only in header-field case and
// order build identical entries and therefore one fingerprint, so such an edit
// never triggers a recreate.
func TestSecretsContentHashCaseOrderInsensitiveAcrossSpecs(t *testing.T) {
	a := envsecret.BuildSecretsFromSpecs(map[string]envsecret.SecretSpec{
		"K": {Value: "v", Host: "h.example", HeaderFields: []string{"X-Api-Key", "Authorization"}},
	}, nil, testUI(t))
	b := envsecret.BuildSecretsFromSpecs(map[string]envsecret.SecretSpec{
		"K": {Value: "v", Host: "h.example", HeaderFields: []string{"authorization", "x-api-key"}},
	}, nil, testUI(t))
	if SecretsContentHash(a) != SecretsContentHash(b) {
		t.Error("case/order-only header_fields change must not flip the fingerprint")
	}
}

func testUI(t *testing.T) termio.UI {
	t.Helper()
	mock := termiotest.NewTestMock(t)
	return &mock
}

func TestSecretsContentHashIncludesHeaderFieldsScope(t *testing.T) {
	// A change to a secret's header scope (same value, same hosts) must
	// produce a different hash so the launcher recreates the VM. See
	// SecretsContentHash.
	allHeaders := SecretsContentHash([]msb.SecretEntry{
		{EnvVar: "K", Value: "v", Allow: []string{"h.example"}},
	})
	scoped := SecretsContentHash([]msb.SecretEntry{
		{
			EnvVar:       "K",
			Value:        "v",
			Allow:        []string{"h.example"},
			Substitution: msb.SecretSubstitution{HeaderFields: []string{"authorization"}},
		},
	})
	if allHeaders == scoped {
		t.Error("SecretsContentHash should differ when Substitution.HeaderFields changes")
	}
}

// TestSecretsContentHashScopedStable pins the digest of a header-scoped secret
// (golden) and asserts a different field set hashes differently. Together they
// prove the folded header-fields segment — both its leading "|" separator and
// the joined names — reaches the digest: dropping either write still leaves a
// scoped secret differing from an all-headers one, so scoped-vs-unscoped alone
// would not notice a scoped-secret edit that stopped recreating the VM. The
// no-scope case is pinned separately by TestSecretsContentHashAllHeadersStable.
func TestSecretsContentHashScopedStable(t *testing.T) {
	const golden = "af3da3e12c4d20cac8cce978b21986bca531ef43c6f5294b5590096d08d4cd22"
	scoped := func(fields ...string) string {
		return SecretsContentHash([]msb.SecretEntry{{
			EnvVar:       "K",
			Value:        "v",
			Allow:        []string{"h.example"},
			Substitution: msb.SecretSubstitution{HeaderFields: fields},
		}})
	}
	if got := scoped("authorization"); got != golden {
		t.Errorf("scoped header-fields hash = %q, want %q", got, golden)
	}
	if scoped("authorization") == scoped("x-api-key") {
		t.Error("SecretsContentHash must differ when the header-field names differ")
	}
}

func TestSecretsChanged(t *testing.T) {
	hash := SecretsContentHash([]msb.SecretEntry{{EnvVar: "A", Value: "1"}})
	if SecretsChanged(SecretState{Hash: hash}, []msb.SecretEntry{{EnvVar: "A", Value: "1"}}) {
		t.Error("SecretsChanged should be false when hashes match")
	}
	if !SecretsChanged(SecretState{Hash: hash}, []msb.SecretEntry{{EnvVar: "A", Value: "2"}}) {
		t.Error("SecretsChanged should be true when a value changes")
	}
	if !SecretsChanged(SecretState{}, []msb.SecretEntry{{EnvVar: "A", Value: "1"}}) {
		t.Error("SecretsChanged with empty applied and non-empty desired should be true")
	}
	if SecretsChanged(SecretState{}, nil) {
		t.Error("SecretsChanged with empty applied and nil desired should be false")
	}
	if SecretsChanged(SecretState{}, []msb.SecretEntry{}) {
		t.Error("SecretsChanged with empty applied and an empty non-nil desired should be false")
	}
}

func TestBuildEnvState(t *testing.T) {
	st := BuildEnvState(map[string]string{"B": "2", "A": "1"})
	if st.Hash != EnvContentHash(map[string]string{"A": "1", "B": "2"}) {
		t.Errorf("BuildEnvState.Hash mismatch")
	}
	if !slices.Equal(st.Entries, []string{"A", "B"}) {
		t.Errorf("BuildEnvState.Entries = %v, want the sorted names [A B] without values", st.Entries)
	}
}

func TestBuildSecretState(t *testing.T) {
	st := BuildSecretState([]msb.SecretEntry{{EnvVar: "B", Value: "2"}, {EnvVar: "A", Value: "1"}})
	if st.Hash != SecretsContentHash([]msb.SecretEntry{{EnvVar: "A", Value: "1"}, {EnvVar: "B", Value: "2"}}) {
		t.Errorf("BuildSecretState.Hash mismatch")
	}
	if !slices.Equal(st.Entries, []string{"A", "B"}) {
		t.Errorf("BuildSecretState.Entries = %v, want sorted [A B]", st.Entries)
	}
}

func TestBuildSecretStateDescribesTheStructureButNeverTheValue(t *testing.T) {
	headers := true
	st := BuildSecretState([]msb.SecretEntry{{
		EnvVar:       "GH_TOKEN",
		Value:        "ghp_supersecret",
		Allow:        []string{"github.com", "api.github.com"},
		Passthrough:  []string{"proxy.example.com"},
		Substitution: msb.SecretSubstitution{Headers: &headers, HeaderFields: []string{"authorization"}},
	}})
	want := []string{
		"GH_TOKEN: hosts api.github.com, github.com; passthrough proxy.example.com; header fields authorization",
	}
	if !slices.Equal(st.Entries, want) {
		t.Errorf("BuildSecretState.Entries = %q, want %q", st.Entries, want)
	}
	for _, entry := range st.Entries {
		if strings.Contains(entry, "ghp_supersecret") {
			t.Fatalf("entry %q leaks the secret value", entry)
		}
	}
}

func TestNetworkChanged(t *testing.T) {
	policy := network.Policy{Profiles: []network.Profile{network.ProfilePublic}}
	fp := policy.Fingerprint()
	if NetworkChanged(NetworkState{Hash: fp}, policy) {
		t.Error("NetworkChanged should be false when hashes match")
	}
	if !NetworkChanged(NetworkState{Hash: fp}, network.Policy{Profiles: []network.Profile{network.ProfileNone}}) {
		t.Error("NetworkChanged should be true when profile changes")
	}
	if !NetworkChanged(NetworkState{}, policy) {
		t.Error("NetworkChanged with empty applied and non-empty desired should be true")
	}
	if !NetworkChanged(NetworkState{}, network.Policy{}) {
		t.Error("NetworkChanged with empty applied and empty desired should migrate to the secure default")
	}
}

// TestNetworkChanged_MigratesPreFixDenyPolicy covers the #231 migration: a VM
// whose persisted NetworkState holds the pre-fix fingerprint of a deny-bearing
// profiled policy (one that omitted the rule-order marker) must be reported as
// changed, so it is recreated once to bake in the corrected emission order. Once
// the post-recreate state is persisted, no further recreation is triggered.
func TestNetworkChanged_MigratesPreFixDenyPolicy(t *testing.T) {
	const preFixDenyPublic = "440e1b21984eb6657c69098f6e9ddd813c90b39ebc3f6f575ad640ec97528498"
	policy := network.Policy{
		Profiles:   []network.Profile{network.ProfilePublic},
		EgressDeny: []string{"123.123.123.0/24"},
	}
	if !NetworkChanged(NetworkState{Hash: preFixDenyPublic}, policy) {
		t.Fatal("a persisted pre-fix deny fingerprint must trigger one recreate")
	}
	if NetworkChanged(BuildNetworkState(policy), policy) {
		t.Fatal("the post-recreate fingerprint must not trigger another recreate")
	}
}

func TestBuildNetworkState(t *testing.T) {
	policy := network.Policy{Profiles: []network.Profile{network.ProfileNone}}
	st := BuildNetworkState(policy)
	if st.Hash != policy.Fingerprint() {
		t.Errorf("BuildNetworkState.Hash = %q, want fingerprint %q", st.Hash, policy.Fingerprint())
	}
	if !slices.Equal(st.Entries, policy.Describe()) {
		t.Errorf("BuildNetworkState.Entries = %v, want %v", st.Entries, policy.Describe())
	}
}

// TestBuildNetworkStateAppliesEffectiveDefault pins that a zero policy records
// its effective default profile name, not an empty list, so the persisted state
// matches the policy the VM is created with.
func TestBuildNetworkStateAppliesEffectiveDefault(t *testing.T) {
	st := BuildNetworkState(network.Policy{})
	if !slices.Equal(st.Entries, []string{"profile: " + network.DefaultProfile.String()}) {
		t.Fatalf("BuildNetworkState(zero).Entries = %v, want the %s profile", st.Entries, network.DefaultProfile)
	}
	if st.Hash != (network.Policy{}).Fingerprint() {
		t.Errorf(
			"BuildNetworkState(zero).Hash = %q, want the effective fingerprint %q",
			st.Hash,
			(network.Policy{}).Fingerprint(),
		)
	}
}

func TestMountsChanged(t *testing.T) {
	desired := mounts.Mounts{
		"/home/dev/.m2": {Source: "/host/.m2"},
	}
	applied := BuildMountState(desired)

	if MountsChanged(applied, desired) {
		t.Error("MountsChanged with identical mounts should be false")
	}
	if !MountsChanged(applied, nil) {
		t.Error("MountsChanged after removing all mounts should be true")
	}
	if !MountsChanged(applied, mounts.Mounts{
		"/home/dev/.m2": {Source: "/host/.m2", Readonly: true},
	}) {
		t.Error("MountsChanged with a flipped readonly flag should be true")
	}
}

// TestMountsChangedEmptyAppliedState covers VMs created before mounts existed:
// no persisted fingerprint must only count as changed when mounts are set.
func TestMountsChangedEmptyAppliedState(t *testing.T) {
	if MountsChanged(MountState{}, nil) {
		t.Error("MountsChanged with no persisted state and no mounts should be false")
	}
	if !MountsChanged(MountState{}, mounts.Mounts{
		"/home/dev/.m2": {Source: "/host/.m2"},
	}) {
		t.Error("MountsChanged with no persisted state and configured mounts should be true")
	}
}

func TestBuildMountState(t *testing.T) {
	desired := mounts.Mounts{
		"/home/dev/z": {Source: "/host/z"},
		"/home/dev/a": {Source: "/host/a"},
	}
	st := BuildMountState(desired)
	if st.Hash != mounts.Fingerprint(desired) {
		t.Errorf("BuildMountState.Hash = %q, want %q", st.Hash, mounts.Fingerprint(desired))
	}
	if !slices.Equal(st.Entries, mounts.Describe(desired)) {
		t.Errorf("BuildMountState.Entries = %v, want %v", st.Entries, mounts.Describe(desired))
	}
}
