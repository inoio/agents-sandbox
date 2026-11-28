package journeys

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v4"

	"github.com/inoio/agents-sandbox/internal/sandbox/envsecret"
)

const (
	secretSpikeKey            = "sk-journeys-secret-substitution-only"
	secretTrustHookPath       = ".journeys/prepare-ca.sh"
	secretCombinedCAGuestPath = "/home/dev/.journeys/combined-ca.pem"
)

// StepSeedSecretStub configures an HTTPS model stub with a host-scoped secret.
// Only env.secret.yaml holds the synthetic key; the adapter reads its env var.
func StepSeedSecretStub() Step {
	return Step{
		Name: "the HTTPS stub uses a host-scoped env secret instead of a plain key",
		Run: func(c *Context) error {
			facts := c.Harness.Secret
			if facts == nil {
				return fmt.Errorf("the %s adapter declares no secret facts", c.Agent)
			}
			provider, err := StartTLSStubProvider(c.TB)
			if err != nil {
				return err
			}
			c.Stub = provider
			c.Harness.Stub = facts.Stub
			seeds := stubSeeds(c)
			seeds.HomeManifest[secretTrustHookPath] = "#!/bin/sh\nset -eu\n" +
				"cat /.msb/tls/ca.pem " + stubCAGuestPath + " > " + secretCombinedCAGuestPath + "\n"
			if err := seeds.Apply(c.Home, ""); err != nil {
				return err
			}
			return seedSecretLauncher(c, *facts)
		},
	}
}

// seedSecretLauncher adds the startup trust hook and the host-only secret file.
// The bundle retains the interception CA as well as the stub's own CA, so a
// trust failure cannot masquerade as an unsupported substitution route.
func seedSecretLauncher(c *Context, facts SecretFacts) error {
	directory := filepath.Join(c.Home, "config", launcherConfigDirName)
	path := filepath.Join(directory, "config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var config map[string]json.RawMessage
	if err = json.Unmarshal(data, &config); err != nil {
		return err
	}
	var manifest map[string]any
	if err = json.Unmarshal(config["home"], &manifest); err != nil {
		return err
	}
	manifest[secretTrustHookPath] = map[string]any{"source": manifest[secretTrustHookPath], "hook": "startup"}
	config["home"], err = json.Marshal(manifest)
	if err != nil {
		return err
	}
	data, err = json.Marshal(config)
	if err != nil {
		return err
	}
	if err = os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	secret, err := yaml.Marshal(map[string]envsecret.SecretSpec{
		facts.EnvVar: {
			Value: secretSpikeKey, Host: stubGatewayHost, HeaderFields: []string{facts.Header},
			Placeholder: envsecret.PlaceholderFor(facts.EnvVar),
		},
	})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(directory, "env.secret.yaml"), secret, 0o600)
}

// StepAssertGuestHasOnlyPlaceholder compares the guest environment inside the
// VM without printing the value or passing the host-only key to the guest.
func StepAssertGuestHasOnlyPlaceholder() Step {
	return AsCheck(Step{Name: "the VM environment holds only the secret placeholder", Run: func(c *Context) error {
		if c.Harness.Secret == nil {
			return errors.New("no secret facts on the adapter")
		}
		facts := *c.Harness.Secret
		active, clients := c.Session, c.Clients
		defer func() { c.Session, c.Clients = active, clients }()
		code, _, err := runInShell(c, `test "$`+facts.EnvVar+`" = `+shellQuote(envsecret.PlaceholderFor(facts.EnvVar)))
		if err != nil {
			return err
		}
		if code != 0 {
			return errors.New(
				"the guest secret env var is absent or differs from the configured placeholder (value withheld)",
			)
		}
		return nil
	}})
}

// StepAssertSecretSubstituted requires a real model turn over TLS authenticated
// with the synthetic host-only key, rather than a model probe or a title.
func StepAssertSecretSubstituted() Step {
	return AsCheck(
		Step{
			Name: "the HTTPS model turn carries the substituted secret, not its placeholder",
			Run: func(c *Context) error {
				if c.Stub == nil || c.Harness.Secret == nil {
					return errors.New("no secret stub or adapter facts")
				}
				return secretEvidenceProblem(c.Stub.Requests(), *c.Harness.Secret)
			},
		},
	)
}

func secretEvidenceProblem(requests []StubRequest, facts SecretFacts) error {
	turns := 0
	for _, request := range requests {
		if request.Method != http.MethodPost || !isTurnPath(request.Path) || request.Side {
			continue
		}
		turns++
		if !request.TLS || request.DecodeError != "" {
			return errors.New("the model turn lacks valid HTTPS transport or a decodable body")
		}
		if request.HasCredential(facts.Header, facts.Prefix+envsecret.PlaceholderFor(facts.EnvVar)) {
			return errors.New(
				"the HTTPS stub received the unchanged placeholder: this route did not substitute the secret",
			)
		}
		if !request.HasCredential(facts.Header, facts.Prefix+secretSpikeKey) {
			return errors.New("the HTTPS model turn carried a missing or unexpected credential (value withheld)")
		}
	}
	if turns == 0 {
		return errors.New("no authenticated HTTPS model turn reached the stub")
	}
	return nil
}
