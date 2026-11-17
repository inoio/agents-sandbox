package readback

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/inoio/agents-sandbox/internal/agent"
)

// CredentialKind classifies a credential the read-back found: a static secret
// whose value is extracted into memory, or a login whose value is never read.
type CredentialKind int

const (
	// CredentialStatic is a secret written as a literal (a dotenv entry, an
	// inline API key). Its value is read into Credential.Value, in memory only.
	CredentialStatic CredentialKind = iota
	// CredentialLogin is an OAuth or session credential. Its presence, source,
	// and provider are recorded; its value is never read out of the store.
	CredentialLogin
)

// Credential is one credential the read-back found in the home volume. Value is
// set for a static credential only, stays in memory, and never reaches a
// manifest or a persisted artefact.
//
//exhaustruct:ignore
type Credential struct {
	Kind     CredentialKind
	Source   string // sandbox-home-relative path, or "<agent>:<probe>"
	Provider string
	Field    string   // header-field hint, optional
	Hosts    []string // endpoints named for the entry, optional
	Value    string   // static only; never in a Manifest
}

// ReadBack is the result of reading one agent's home back: the declared config
// paths that exist, the credentials found, and the keys changed since the
// baseline manifest.
type ReadBack struct {
	ConfigPaths []string
	Static      []Credential
	Login       []Credential
	Changed     []string
}

// Read inspects the agent's declared config and credential paths under home and
// reports what it found, comparing the current manifest against baseline. An
// agent without a ReadBackDescriber (unknown, or not yet verified) yields an
// empty ReadBack, never an error.
func Read(home, agentName string, baseline Manifest) (ReadBack, error) {
	a, ok := agent.Lookup(agentName)
	if !ok {
		return ReadBack{}, nil
	}
	if _, ok := agent.AsReadBackDescriber(a); !ok {
		return ReadBack{}, nil
	}

	current, err := TakeManifest(home, agentName)
	if err != nil {
		return ReadBack{}, err
	}

	readBack := ReadBack{ConfigPaths: nil, Static: nil, Login: nil, Changed: current.Changed(baseline)}
	for _, entry := range current.Entries {
		if isProbe(entry) {
			// A credential probe's entry carries no path: the probe itself
			// supplies the credentials, below.
			continue
		}
		loginProvider, isLogin := loginCredentialProviders[entry.Key]
		switch {
		case isLogin:
			readBack.Login = append(readBack.Login, Credential{
				Kind:     CredentialLogin,
				Source:   entry.Key,
				Provider: loginProvider,
			})
		case filepath.Base(entry.Key) == dotenvFileName:
			content, err := os.ReadFile(filepath.Join(home, entry.Key))
			if err != nil {
				continue
			}
			readBack.Static = append(readBack.Static, staticCredentials(entry.Key, content)...)
		default:
			readBack.ConfigPaths = append(readBack.ConfigPaths, entry.Key)
		}
	}
	readBack.appendProbeCredentials(home, agentName)
	sort.Strings(readBack.ConfigPaths)
	return readBack, nil
}

// appendProbeCredentials runs the named agent's credential probes (its agent
// database, which holds a credential no declared path exposes) and appends what
// they found. A probe whose store could not be read contributes nothing: the
// read-back stays best-effort, and the manifest's sentinel digest records that
// the probe failed. The same reader produces both, so the two cannot disagree.
func (r *ReadBack) appendProbeCredentials(home, agentName string) {
	for _, probe := range declaredCredentialProbes(agentName) {
		for _, credential := range runCredentialProbe(home, probe).Credentials {
			if credential.Kind == CredentialLogin {
				r.Login = append(r.Login, credential)
				continue
			}
			r.Static = append(r.Static, credential)
		}
	}
}

// dotenvFileName is the static-secret file name: a declared path with this base
// is parsed as KEY=VALUE lines whose values are static credentials.
const dotenvFileName = ".env"

// loginCredentialProviders maps a declared login store (sandbox-home-relative
// path) to the provider its credential authenticates. A login store's value is
// never read: the read-back records only that the store exists.
var loginCredentialProviders = map[string]string{
	".claude/settings.json":           "claude-code",
	".codex/auth.json":                "codex",
	".local/share/opencode/auth.json": "opencode",
	".pi/agent/settings.json":         "pi",
}

// staticCredentials parses a dotenv file's KEY=VALUE lines into static
// credentials. Blank lines and # comments are skipped; the value comes from the
// right-hand side, and Provider/Field are derived from a recognisable key name.
func staticCredentials(source string, content []byte) []Credential {
	var credentials []Credential
	for line := range strings.SplitSeq(string(content), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		provider, field := staticCredentialProviderField(key)
		credentials = append(credentials, Credential{
			Kind:     CredentialStatic,
			Source:   source,
			Provider: provider,
			Field:    field,
			Value:    strings.TrimSpace(value),
		})
	}
	return credentials
}

// staticCredentialProviderField derives a provider and header-field hint from a
// dotenv key when the key names an API key variable (OPENAI_API_KEY → provider
// "openai", field "authorization"). Both are empty for an unrecognised key.
func staticCredentialProviderField(key string) (string, string) {
	name, ok := strings.CutSuffix(key, "_API_KEY")
	if !ok || name == "" {
		return "", ""
	}
	return strings.ToLower(name), "authorization"
}
