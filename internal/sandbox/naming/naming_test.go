package naming

import "testing"

func TestArtifactFor_Dispatch(t *testing.T) {
	cases := []struct {
		in     string
		slug   string
		digest string
		agent  string
	}{
		{"agents-sandbox-vm-agents-sandbox-1mjusbm3wikhb0", "agents-sandbox-1mjusbm3wikhb0", "", ""},
		{"agents-sandbox-vm-agents-sandbox-1mjusbm3wikhb0-main", "agents-sandbox-1mjusbm3wikhb0", "", "main"},
		{"agents-sandbox/runner-myproject:xYz1234AbCdEfGh", "myproject", "xYz1234AbCdEfGh", ""},
		{"agents-sandbox/runner-myproject:latest", "myproject", "", ""},
		{"agents-sandbox/runner-myproject", "myproject", "", ""},
		{
			"agents-sandbox-home-myproject-aB3cDe4fGhIjKl-xYz1234AbCdEfGh",
			"myproject-aB3cDe4fGhIjKl",
			"xYz1234AbCdEfGh",
			"",
		},
	}
	for _, c := range cases {
		info := ArtifactFor(c.in)
		if info.Slug != c.slug || info.Digest != c.digest || info.Agent != c.agent {
			t.Errorf(
				"%q: got (%q,%q,%q) want (%q,%q,%q)",
				c.in,
				info.Slug,
				info.Digest,
				info.Agent,
				c.slug,
				c.digest,
				c.agent,
			)
		}
	}
}

func TestArtifactFor_SinglePartSlugs(t *testing.T) {
	cases := []struct {
		in     string
		slug   string
		digest string
	}{
		{"agents-sandbox-task-prefill-proj-1719432000", "prefill-proj", ""},
		{"agents-sandbox-task-fill-proj", "fill", ""},
		{"agents-sandbox-task-prefill", "prefill", ""},
	}
	for _, c := range cases {
		info := ArtifactFor(c.in)
		if info.Slug != c.slug || info.Digest != c.digest {
			t.Errorf("%q: got (%q,%q) want (%q,%q)", c.in, info.Slug, info.Digest, c.slug, c.digest)
		}
	}
}

func TestArtifactFor_Unrecognized(t *testing.T) {
	cases := []struct {
		in         string
		wantSlug   string
		wantDigest string
	}{
		{"some-random-name", "", ""},
		{"", "", ""},
		{"agents-sandbox-other-myslug", "", ""},
		{"agents-sandbox-vm-", "", ""},
		{"agents-sandbox-home-", "", ""},
	}
	for _, c := range cases {
		info := ArtifactFor(c.in)
		if info.Slug != c.wantSlug || info.Digest != c.wantDigest {
			t.Errorf("%q: got (%q,%q) want (%q,%q)", c.in, info.Slug, info.Digest, c.wantSlug, c.wantDigest)
		}
	}
}

func TestHashSuffix(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"no hash - simple name", "projectname-main", -1},
		{"hash at hyphen position 5", "agents-sandbox-1mjusbm3wikhb0", 15},
		{"hash followed by branch", "agents-sandbox-1mjusbm3wikhb0-main", 15},
		{"hash embedded in multi-dash slug", "my-project-1mjusbm3wikhb0-develop", 11},
		{"no hash - short string", "abc-def", -1},
	}
	for _, c := range cases {
		got := findHashSuffix(c.in)
		if got != c.want {
			t.Errorf("findHashSuffix(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestParseImageTag(t *testing.T) {
	cases := []struct {
		in         string
		wantSlug   string
		wantDigest string
		wantAgent  string
	}{
		{"agents-sandbox/runner-myproject:xYz1234AbCdEfGh", "myproject", "xYz1234AbCdEfGh", ""},
		{"agents-sandbox/runner-myproject:latest", "myproject", "", ""},
		{"agents-sandbox/runner-myproject:", "myproject", "", ""},
		{"agents-sandbox/runner-myproject", "myproject", "", ""},
		{"agents-sandbox/runner-my-project-name:xYz1234AbCdEfGh", "my-project-name", "xYz1234AbCdEfGh", ""},
		{"agents-sandbox/runner-myproject:sha256:abc123", "myproject:sha256", "abc123", ""},
		{"agents-sandbox/runner-myproject:opencode-latest", "myproject", "", "opencode"},
		{"agents-sandbox/runner-myproject:pi-latest", "myproject", "", "pi"},
		{"agents-sandbox/runner-myproject:claude-code-latest", "myproject", "", "claude-code"},
		{"other-image/myproject:tag", "", "", ""},
	}
	for _, c := range cases {
		info := ParseImageTag(c.in)
		if info.Slug != c.wantSlug {
			t.Errorf("ParseImageTag(%q) slug = %q, want %q", c.in, info.Slug, c.wantSlug)
		}
		if info.Digest != c.wantDigest {
			t.Errorf("ParseImageTag(%q) digest = %q, want %q", c.in, info.Digest, c.wantDigest)
		}
		if info.Agent != c.wantAgent {
			t.Errorf("ParseImageTag(%q) agent = %q, want %q", c.in, info.Agent, c.wantAgent)
		}
	}
}

func TestParseVMName(t *testing.T) {
	cases := []struct {
		in        string
		wantSlug  string
		wantAgent string
	}{
		{"agents-sandbox-vm-agents-sandbox-1mjusbm3wikhb0", "agents-sandbox-1mjusbm3wikhb0", ""},
		{"agents-sandbox-vm-agents-sandbox-1mjusbm3wikhb0-main", "agents-sandbox-1mjusbm3wikhb0", "main"},
		{"agents-sandbox-vm-my-project-1mjusbm3wikhb0-develop", "my-project-1mjusbm3wikhb0", "develop"},
		{"agents-sandbox-vm-projectname-main", "projectname-main", ""},
		{"agents-sandbox-vm-myproject-abc1234567890", "myproject-abc1234567890", ""},
		{"agents-sandbox-vm-noHash", "noHash", ""},
		{"agents-sandbox-home-test", "", ""},
		{"agents-sandbox-vm-projectname-aB3cDe4fGhIjKl", "projectname-aB3cDe4fGhIjKl", ""},
		{"agents-sandbox-vm-projectname-aB3cDe4fGhIjKl-feature", "projectname-aB3cDe4fGhIjKl-feature", ""},
	}
	for _, c := range cases {
		info := ParseVMName(c.in)
		if info.Slug != c.wantSlug {
			t.Errorf("ParseVMName(%q) slug = %q, want %q", c.in, info.Slug, c.wantSlug)
		}
		if info.Agent != c.wantAgent {
			t.Errorf("ParseVMName(%q) agent = %q, want %q", c.in, info.Agent, c.wantAgent)
		}
	}
}

func TestParseHomeVolumeName(t *testing.T) {
	cases := []struct {
		in         string
		wantSlug   string
		wantDigest string
	}{
		{
			"agents-sandbox-home-myproject-aB3cDe4fGhIjKl-xYz1234AbCdEfGh",
			"myproject-aB3cDe4fGhIjKl",
			"xYz1234AbCdEfGh",
		},
		{"agents-sandbox-home-myproject-abc1234567890", "myproject", "abc1234567890"},
		{"agents-sandbox-home-abc-def-gh", "abc-def", "gh"},
		{"agents-sandbox-vm-something", "", ""},
	}
	for _, c := range cases {
		info := ParseHomeVolumeName(c.in)
		if info.Slug != c.wantSlug {
			t.Errorf("ParseHomeVolumeName(%q) slug = %q, want %q", c.in, info.Slug, c.wantSlug)
		}
		if info.Digest != c.wantDigest {
			t.Errorf("ParseHomeVolumeName(%q) digest = %q, want %q", c.in, info.Digest, c.wantDigest)
		}
	}
}

func TestParseHomeVolumeNameNewFormat(t *testing.T) {
	cases := []struct {
		in       string
		wantSlug string
	}{
		{"agents-sandbox-home-myproject-1mjusbm3wikhb0-20260812T123456", "myproject-1mjusbm3wikhb0"},
		{"agents-sandbox-home-projectname-20260101T000000", "projectname"},
	}
	for _, c := range cases {
		info := ParseHomeVolumeName(c.in)
		if info.Slug != c.wantSlug {
			t.Errorf("ParseHomeVolumeName(%q) slug = %q, want %q", c.in, info.Slug, c.wantSlug)
		}
	}
}

func TestArtifactFor(t *testing.T) {
	cases := []struct {
		in         string
		wantSlug   string
		wantDigest string
	}{
		{"agents-sandbox-vm-agents-sandbox-1mjusbm3wikhb0", "agents-sandbox-1mjusbm3wikhb0", ""},
		{"agents-sandbox/runner-myproject:xYz1234AbCdEfGh", "myproject", "xYz1234AbCdEfGh"},
		{
			"agents-sandbox-home-myproject-aB3cDe4fGhIjKl-xYz1234AbCdEfGh",
			"myproject-aB3cDe4fGhIjKl",
			"xYz1234AbCdEfGh",
		},
		{"agents-sandbox-task-prefill-proj-1719432000", "prefill-proj", ""},
		{"unknown-prefix-foo", "", ""},
	}
	for _, c := range cases {
		info := ArtifactFor(c.in)
		if info.Slug != c.wantSlug {
			t.Errorf("ArtifactFor(%q) slug = %q, want %q", c.in, info.Slug, c.wantSlug)
		}
		if info.Digest != c.wantDigest {
			t.Errorf("ArtifactFor(%q) digest = %q, want %q", c.in, info.Digest, c.wantDigest)
		}
	}
}

func TestParseVMNameAgent(t *testing.T) {
	cases := []struct {
		in        string
		wantSlug  string
		wantAgent string
	}{
		{"agents-sandbox-vm-myproject-1mjusbm3wikhb0", "myproject-1mjusbm3wikhb0", ""},
		{"agents-sandbox-vm-myproject-1mjusbm3wikhb0-opencode", "myproject-1mjusbm3wikhb0", "opencode"},
		{"agents-sandbox-vm-myproject-1mjusbm3wikhb0-pi", "myproject-1mjusbm3wikhb0", "pi"},
		{"agents-sandbox-vm-projectname-main", "projectname-main", ""},
		{"agents-sandbox-vm-noHash", "noHash", ""},
	}
	for _, c := range cases {
		info := ParseVMName(c.in)
		if info.Slug != c.wantSlug || info.Agent != c.wantAgent {
			t.Errorf("ParseVMName(%q) = (slug=%q agent=%q), want (slug=%q agent=%q)",
				c.in, info.Slug, info.Agent, c.wantSlug, c.wantAgent)
		}
	}
}

func TestParseHomeVolumeNameAgent(t *testing.T) {
	cases := []struct {
		in        string
		wantSlug  string
		wantAgent string
	}{
		// New format: home-<slug>-<agent>-<ts>.
		{
			"agents-sandbox-home-myproject-1mjusbm3wikhb0-opencode-20260812T123456",
			"myproject-1mjusbm3wikhb0",
			"opencode",
		},
		{"agents-sandbox-home-myproject-1mjusbm3wikhb0-pi-20260812T123456", "myproject-1mjusbm3wikhb0", "pi"},
		// Legacy new-format: home-<slug>-<ts> (segment before ts is the slug hash).
		{"agents-sandbox-home-myproject-1mjusbm3wikhb0-20260812T123456", "myproject-1mjusbm3wikhb0", ""},
		// Legacy digest format: home-<slug>-<digest>.
		{"agents-sandbox-home-myproject-1mjusbm3wikhb0-xYz1234AbCdEfGh", "myproject-1mjusbm3wikhb0", ""},
	}
	for _, c := range cases {
		info := ParseHomeVolumeName(c.in)
		if info.Slug != c.wantSlug || info.Agent != c.wantAgent {
			t.Errorf("ParseHomeVolumeName(%q) = (slug=%q agent=%q), want (slug=%q agent=%q)",
				c.in, info.Slug, info.Agent, c.wantSlug, c.wantAgent)
		}
	}
}

func TestIsBase36Hash(t *testing.T) {
	for _, ok := range []string{"1mjusbm3wikhb0", "a3b4c5d6e7f8g9"} {
		if !isBase36Hash(ok) {
			t.Errorf("isBase36Hash(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"", "short", "not_a_hash_1234", "1234567890123Z", "a"} {
		if isBase36Hash(bad) {
			t.Errorf("isBase36Hash(%q) = true, want false", bad)
		}
	}
}

func TestIsHomeTimestamp(t *testing.T) {
	for _, ok := range []string{"20260812T123456", "30100101T000000"} {
		if !isHomeTimestamp(ok) {
			t.Errorf("isHomeTimestamp(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"", "20260812T12A456", "10260812T123456", "20260812X123456", "short"} {
		if isHomeTimestamp(bad) {
			t.Errorf("isHomeTimestamp(%q) = true, want false", bad)
		}
	}
}
