package journeys

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/inoio/agents-sandbox/internal/sandbox/readback"
)

func TestOpencode2AdapterSeedsTheSameProviderAsHostConfig(t *testing.T) {
	const addr = "http://stub.test:1234"
	provisioned := opencode2Harness.StubConfig(addr)[opencodeStubConfigTarget]
	host := opencode2Harness.HostConfig(addr)[opencodeHostConfigRelPath]
	if provisioned == "" || provisioned != host {
		t.Errorf("provisioned = %q, host = %q; want the same provider document in both", provisioned, host)
	}
	if got, want := providerBaseURL(t, provisioned), addr+"/v1"; got != want {
		t.Errorf("baseURL = %q, want %q", got, want)
	}
}

// TestOpencodeProviderDocumentCarriesAnInlineCredential pins the credential a
// learn-mode journey observes: the provider document's options carry a literal
// apiKey, which the read-back classifies as a static credential scoped by the
// derived env-var name <PROVIDERID>_API_KEY (stubCredentialEnvVar) and the
// entry's baseURL host. Without it the UC1 secret assertion has nothing to find.
func TestOpencodeProviderDocumentCarriesAnInlineCredential(t *testing.T) {
	provider, err := StartStubProvider(t)
	if err != nil {
		t.Fatalf("StartStubProvider: %v", err)
	}
	t.Cleanup(provider.Stop)

	content := opencode2Harness.StubConfig(provider.Addr())[opencodeStubConfigTarget]
	var document struct {
		Provider map[string]struct {
			Options struct {
				APIKey string `json:"apiKey"`
			} `json:"options"`
		} `json:"provider"`
	}
	if err := json.Unmarshal([]byte(content), &document); err != nil {
		t.Fatalf("decode provider config %q: %v", content, err)
	}
	if document.Provider[stubProviderID].Options.APIKey == "" {
		t.Fatalf("provider %q carries no inline apiKey: %q", stubProviderID, content)
	}
}

// TestOpencodeProviderDocumentClassifiesAsTheAssertedCredential pins the journey's
// premise without a VM: the read-back classifies the stub provider document's
// inline apiKey as a static credential named exactly what UC1 asserts
// (uc1Scope), scoped to the advertised host. It ties the stub's document to the
// assertion, so a change to the provider id or the options shape fails here
// rather than only on a host accept.
func TestOpencodeProviderDocumentClassifiesAsTheAssertedCredential(t *testing.T) {
	provider, err := StartStubProvider(t)
	if err != nil {
		t.Fatalf("StartStubProvider: %v", err)
	}
	t.Cleanup(provider.Stop)

	content := opencode2Harness.StubConfig(provider.Addr())[opencodeStubConfigTarget]
	credentials := readback.InlineCredentialsIn(opencodeStubConfigTarget, []byte(content))
	var found bool
	for _, credential := range credentials {
		if credential.Kind != readback.CredentialStatic || credential.EnvVar != uc1Scope.EnvVar {
			continue
		}
		found = true
		if !slices.Equal(credential.Hosts, uc1Scope.Hosts) {
			t.Errorf("credential hosts = %v, want %v", credential.Hosts, uc1Scope.Hosts)
		}
	}
	if !found {
		t.Fatalf("InlineCredentialsIn found no static credential %q in %q", uc1Scope.EnvVar, content)
	}
}
