package reprovision

import (
	"context"
	"testing"

	"github.com/inoio/agents-sandbox/internal/sandbox/msb/msbtest"
)

// apiKeyAuth is the provisioned API-key-mode store a keep-login entry writes.
const apiKeyAuth = `{"auth_mode":"apikey","OPENAI_API_KEY":"$MSB_OPENAI_API_KEY"}`

const authPath = "/home/dev/.codex/auth.json"

// provisionAuth provisions the keep-login auth.json over the sandbox's current
// content and reports whether it was written.
func provisionAuth(t *testing.T, current map[string][]byte) bool {
	t.Helper()
	fs := msbtest.NewTestFS(current)
	sb := &msbtest.MockSandbox{FSValue_: fs}
	cf := &ConfigFiles{
		HomeFiles: map[string][]byte{authPath: []byte(apiKeyAuth)},
		KeepLogin: map[string]bool{authPath: true},
	}
	if err := Provision(context.Background(), sb, cf); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	return fs.Writes[authPath] != nil
}

func TestProvisionKeepsARenewingLoginAtAKeepLoginTarget(t *testing.T) {
	for name, current := range map[string]string{
		"codex chatgpt login": `{"auth_mode":"chatgpt","OPENAI_API_KEY":null,` +
			`"tokens":{"access_token":"a","refresh_token":"r"}}`,
		"camel-case refresh token": `{"claudeAiOauth":{"accessToken":"a","refreshToken":"r"}}`,
		"refresh token in a list":  `{"logins":[{"refresh_token":"r"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if provisionAuth(t, map[string][]byte{authPath: []byte(current)}) {
				t.Error("a renewing login was overwritten")
			}
		})
	}
}

func TestProvisionWritesAKeepLoginTargetWithoutARenewingLogin(t *testing.T) {
	for name, current := range map[string]map[string][]byte{
		"absent":               nil,
		"plain-text api key":   {authPath: []byte(`{"auth_mode":"apikey","OPENAI_API_KEY":"sk-proj-real"}`)},
		"empty refresh token":  {authPath: []byte(`{"tokens":{"refresh_token":""}}`)},
		"refresh token number": {authPath: []byte(`{"refresh_token":7}`)},
		"not json":             {authPath: []byte(`OPENAI_API_KEY=x`)},
	} {
		t.Run(name, func(t *testing.T) {
			if !provisionAuth(t, current) {
				t.Error("the keep-login target was not written")
			}
		})
	}
}

func TestProvisionOverwritesAPlainHomeFileRegardlessOfItsContent(t *testing.T) {
	fs := msbtest.NewTestFS(map[string][]byte{authPath: []byte(`{"refresh_token":"r"}`)})
	sb := &msbtest.MockSandbox{FSValue_: fs}
	cf := &ConfigFiles{HomeFiles: map[string][]byte{authPath: []byte(apiKeyAuth)}}

	if err := Provision(context.Background(), sb, cf); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if fs.Writes[authPath] == nil {
		t.Error("a home file without keep-login was not written")
	}
}
