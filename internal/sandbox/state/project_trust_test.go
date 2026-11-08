package state

import (
	"reflect"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths/configpathstest"
)

// TestApprovedProjectLayerRoundTripsPerCheckout pins that an approval is kept
// per checkout and survives the root state write, next to the fields the
// approval does not own.
func TestApprovedProjectLayerRoundTripsPerCheckout(t *testing.T) {
	configpathstest.WithMockConfigPaths(t)
	approval := ApprovedProjectLayer{
		Digest: "abc",
		Inputs: []TrustedInput{
			{Path: "/repo/.agents-sandbox/config.yaml", Kind: "launcher config", Entries: []string{`memory: "8G"`}},
		},
	}
	seed := RootState{AdoptedHomeVolumes: map[string]AdoptedHomeVolume{"x/opencode": {CheckoutSlug: "c", Volume: "v"}}}
	seed.ApproveProjectLayer("checkout-a", approval)
	if err := writeRootState(seed); err != nil {
		t.Fatalf("writeRootState: %v", err)
	}

	root, err := ReadRootState()
	if err != nil {
		t.Fatalf("ReadRootState: %v", err)
	}
	got, ok := root.ApprovedProjectLayer("checkout-a")
	if !ok || !reflect.DeepEqual(got, approval) {
		t.Errorf("ApprovedProjectLayer(checkout-a) = %+v, %v; want %+v", got, ok, approval)
	}
	if _, ok := root.ApprovedProjectLayer("checkout-b"); ok {
		t.Error("another checkout reads an approval it never had")
	}
	if len(root.AdoptedHomeVolumes) != 1 {
		t.Errorf("the approval write dropped another root field: %+v", root.AdoptedHomeVolumes)
	}
}
