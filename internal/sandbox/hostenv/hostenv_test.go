package hostenv_test

import (
	"errors"
	"testing"

	"github.com/inoio/agents-sandbox/internal/configpaths"
	"github.com/inoio/agents-sandbox/internal/sandbox/hostenv"
	"github.com/inoio/agents-sandbox/internal/testutil"
)

var errReadlink = errors.New("readlink failed")

func fakeSources(tz string, readlink func(string) (string, error), name, email string) hostenv.Sources {
	return hostenv.Sources{
		Getenv: func(key string) string {
			if key == "TZ" {
				return tz
			}
			return ""
		},
		Readlink:    readlink,
		GitIdentity: func() (string, string) { return name, email },
	}
}

// localeSources builds Sources whose locale variables are set individually, so
// a test can tell which of LC_ALL/LC_CTYPE/LANG the resolver consults.
func localeSources(lcAll, lcCtype, lang string) hostenv.Sources {
	return hostenv.Sources{
		Getenv: func(key string) string {
			switch key {
			case "LC_ALL":
				return lcAll
			case "LC_CTYPE":
				return lcCtype
			case "LANG":
				return lang
			}
			return ""
		},
		Readlink:    failReadlink,
		GitIdentity: func() (string, string) { return "", "" },
	}
}

func linkTo(target string) func(string) (string, error) {
	return func(string) (string, error) { return target, nil }
}

func failReadlink(string) (string, error) { return "", errReadlink }

func TestResolveTimeZone(t *testing.T) {
	tests := []struct {
		name     string
		tz       string
		readlink func(string) (string, error)
		want     string
	}{
		{"env wins verbatim", "Europe/Paris", linkTo("/usr/share/zoneinfo/Etc/UTC"), "Europe/Paris"},
		{"linux symlink", "", linkTo("/usr/share/zoneinfo/Europe/Berlin"), "Europe/Berlin"},
		{"macos symlink", "", linkTo("/var/db/timezone/zoneinfo/Europe/Berlin"), "Europe/Berlin"},
		{"readlink error", "", failReadlink, ""},
		{"target without zoneinfo", "", linkTo("/etc/foo/localtime"), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hostenv.Resolve(fakeSources(tt.tz, tt.readlink, "", ""), false)
			if got["TZ"] != tt.want {
				t.Errorf("TZ = %q, want %q", got["TZ"], tt.want)
			}
		})
	}
}

func TestResolveLocale(t *testing.T) {
	tests := []struct {
		name    string
		lcAll   string
		lcCtype string
		lang    string
		want    string
	}{
		{"lang utf-8", "", "", "en_US.UTF-8", "C.UTF-8"},
		{"lang utf8 no dash", "", "", "en_US.utf8", "C.UTF-8"},
		{"lc_ctype overrides lang", "", "C", "en_US.UTF-8", ""},
		{"lc_all overrides lc_ctype", "C", "en_US.UTF-8", "en_US.UTF-8", ""},
		{"lc_all utf-8 wins", "en_US.UTF-8", "C", "C", "C.UTF-8"},
		{"c locale", "", "", "C", ""},
		{"unset", "", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hostenv.Resolve(localeSources(tt.lcAll, tt.lcCtype, tt.lang), false)
			if got["LANG"] != tt.want {
				t.Errorf("LANG = %q, want %q", got["LANG"], tt.want)
			}
		})
	}
}

func TestResolveGitIdentity(t *testing.T) {
	resolved := hostenv.Resolve(fakeSources("", failReadlink, "Ada", "ada@example.com"), false)
	for _, key := range []string{"GIT_AUTHOR_NAME", "GIT_COMMITTER_NAME"} {
		if resolved[key] != "Ada" {
			t.Errorf("%s = %q, want Ada", key, resolved[key])
		}
	}
	for _, key := range []string{"GIT_AUTHOR_EMAIL", "GIT_COMMITTER_EMAIL"} {
		if resolved[key] != "ada@example.com" {
			t.Errorf("%s = %q, want ada@example.com", key, resolved[key])
		}
	}

	partial := hostenv.Resolve(fakeSources("", failReadlink, "Ada", ""), false)
	if len(partial) != 0 {
		t.Errorf("partial identity produced %v, want no git vars", partial)
	}

	suppressed := hostenv.Resolve(fakeSources("", failReadlink, "Ada", "ada@example.com"), true)
	if _, ok := suppressed["GIT_AUTHOR_NAME"]; ok {
		t.Errorf("a provisioned git config did not suppress the identity: %v", suppressed)
	}
}

func TestResolveUnresolvableHostProducesNoDefaults(t *testing.T) {
	if got := hostenv.Resolve(fakeSources("", failReadlink, "", ""), false); len(got) != 0 {
		t.Errorf("Resolve on an unresolvable host = %v, want empty", got)
	}
}

func TestUnderKeepsBaseValues(t *testing.T) {
	got := hostenv.Under(
		map[string]string{"TZ": "From/File"},
		map[string]string{"TZ": "Host/Zone", "LANG": "C.UTF-8"},
	)
	if got["TZ"] != "From/File" {
		t.Errorf("TZ = %q, want the base value From/File", got["TZ"])
	}
	if got["LANG"] != "C.UTF-8" {
		t.Errorf("LANG = %q, want the default C.UTF-8", got["LANG"])
	}
}

func TestProvisionedGitConfig(t *testing.T) {
	provisioned := t.TempDir()
	testutil.WriteFile(t, provisioned, "config.yaml", "home:\n  .gitconfig: /host/.gitconfig\n")
	provisionedXDG := t.TempDir()
	testutil.WriteFile(t, provisionedXDG, "config.yaml", "home:\n  .config/git/config: /host/gitconfig\n")
	provisionedDotted := t.TempDir()
	testutil.WriteFile(t, provisionedDotted, "config.yaml", "home:\n  ./.gitconfig: /host/.gitconfig\n")
	other := t.TempDir()
	testutil.WriteFile(t, other, "config.yaml", "home:\n  .inputrc:\n")
	empty := t.TempDir()

	tests := []struct {
		name  string
		scope configpaths.ConfigScope
		want  bool
	}{
		{"dot-gitconfig", configpaths.ConfigScope{Dir: provisioned}, true},
		{"xdg git config", configpaths.ConfigScope{Dir: provisionedXDG}, true},
		{"dotted target", configpaths.ConfigScope{Dir: provisionedDotted}, true},
		{"unrelated home entry", configpaths.ConfigScope{Dir: other}, false},
		{"absent config", configpaths.ConfigScope{Dir: empty}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hostenv.ProvisionedGitConfig([]configpaths.ConfigScope{tt.scope}); got != tt.want {
				t.Errorf("ProvisionedGitConfig = %v, want %v", got, tt.want)
			}
		})
	}
}
