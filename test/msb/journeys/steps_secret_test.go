package journeys

import (
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v4"

	"github.com/inoio/agents-sandbox/internal/sandbox/envsecret"
)

func TestSecretStubKeepsTheKeyOutOfProvisionedFiles(t *testing.T) {
	c := &Context{TB: t, Agent: opencode2AgentName, Harness: opencode2Harness, Home: t.TempDir()}
	if err := StepSeedSecretStub().Run(c); err != nil {
		t.Fatal(err)
	}
	if err := StepConfigureLauncherEnv().Run(c); err != nil {
		t.Fatal(err)
	}
	secretFile := filepath.Join(c.Home, "config", "agents-sandbox", "env.secret.yaml")
	data, err := os.ReadFile(secretFile)
	if err != nil {
		t.Fatal(err)
	}
	var secrets map[string]envsecret.SecretSpec
	if err := yaml.Unmarshal(data, &secrets); err != nil {
		t.Fatal(err)
	}
	secret := secrets[stubCredentialEnvVar]
	if len(secrets) != 1 || secret.Value != secretSpikeKey || secret.Host != stubGatewayHost ||
		secret.Placeholder != envsecret.PlaceholderFor(stubCredentialEnvVar) ||
		strings.Join(secret.HeaderFields, ",") != "authorization" {
		t.Fatal("the secret was not scoped to the stub and its auth header")
	}
	if err := filepath.WalkDir(c.Home, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || path == secretFile {
			return walkErr
		}
		content, readErr := os.ReadFile(path)
		if readErr == nil &&
			(strings.Contains(string(content), secretSpikeKey) || strings.Contains(string(content), stubProviderAPIKey)) {
			t.Errorf("a plain credential reached %s", path)
		}
		return readErr
	}); err != nil {
		t.Fatal(err)
	}
	provider, err := os.ReadFile(filepath.Join(c.Home, "seed", opencodeStubConfigTarget))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(provider), "{env:"+stubCredentialEnvVar+"}") ||
		!strings.HasPrefix(c.Stub.Addr(), "https://") {
		t.Fatal("the harness does not use a secret env reference and HTTPS")
	}
	var config struct {
		Home map[string]json.RawMessage `json:"home"`
	}
	data, err = os.ReadFile(filepath.Join(c.Home, "config", "agents-sandbox", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	var hook struct {
		Hook string `json:"hook"`
	}
	if err := json.Unmarshal(config.Home[secretTrustHookPath], &hook); err != nil || hook.Hook != "startup" {
		t.Fatal("the combined CA bundle is not prepared before the harness starts")
	}
	trustHook, err := os.ReadFile(filepath.Join(c.Home, "seed", secretTrustHookPath))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(trustHook), "/.msb/tls/ca.pem") ||
		!strings.Contains(string(trustHook), stubCAGuestPath) {
		t.Fatal("the trust hook drops the runtime or stub CA")
	}
	environment, err := os.ReadFile(filepath.Join(c.Home, "config", "agents-sandbox", "env"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(environment), stubCAEnv+"="+secretCombinedCAGuestPath) {
		t.Fatal("the harness does not trust the combined bundle")
	}
}

func TestSecretEvidenceRequiresAnAuthenticatedTLSTurn(t *testing.T) {
	facts := *opencode2Harness.Secret
	request := StubRequest{
		Method: http.MethodPost, Path: "/v1/chat/completions", Model: stubProviderModel, TLS: true,
		authorization: sha256.Sum256([]byte(facts.Prefix + secretSpikeKey)),
	}
	if err := secretEvidenceProblem([]StubRequest{request}, facts); err != nil {
		t.Fatal(err)
	}
	for _, variant := range []string{"no requests", "probe", "side", "plain HTTP", "placeholder", "wrong key", "decode error"} {
		t.Run(variant, func(t *testing.T) {
			changed := request
			requests := []StubRequest{changed}
			switch variant {
			case "no requests":
				requests = nil
			case "probe":
				changed.Method, changed.Path = http.MethodGet, "/v1/models"
			case "side":
				changed.Side = true
			case "plain HTTP":
				changed.TLS = false
			case "placeholder":
				changed.authorization = sha256.Sum256([]byte(facts.Prefix + envsecret.PlaceholderFor(facts.EnvVar)))
			case "wrong key":
				changed.authorization = sha256.Sum256([]byte("wrong-key"))
			case "decode error":
				changed.DecodeError = "invalid body"
			}
			if requests != nil {
				requests[0] = changed
			}
			if err := secretEvidenceProblem(requests, facts); err == nil {
				t.Fatal("non-evidence passed the substitution check")
			}
		})
	}
}

func TestSecretStubRequiresAnAdapter(t *testing.T) {
	if err := StepSeedSecretStub().Run(&Context{TB: t, Home: t.TempDir()}); err == nil {
		t.Fatal("the secret stub seeded without an adapter")
	}
}

func TestSecretEvidenceRejectsAMixtureOfGoodAndBadTurns(t *testing.T) {
	facts := *opencode2Harness.Secret
	valid := StubRequest{
		Method: http.MethodPost, Path: "/v1/chat/completions", TLS: true,
		authorization: sha256.Sum256([]byte(facts.Prefix + secretSpikeKey)),
	}
	invalid := valid
	invalid.authorization = sha256.Sum256([]byte(facts.Prefix + envsecret.PlaceholderFor(facts.EnvVar)))
	for _, requests := range [][]StubRequest{{valid, invalid}, {invalid, valid}} {
		problem := secretEvidenceProblem(requests, facts)
		if problem == nil || !strings.Contains(problem.Error(), "unchanged placeholder") {
			t.Fatal("a substituted turn masked a turn carrying the placeholder")
		}
		if strings.Contains(problem.Error(), secretSpikeKey) {
			t.Fatal("the diagnostic exposed the key")
		}
	}
}
