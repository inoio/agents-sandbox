package naming

import "testing"

func TestArtifactFor_Dispatch(t *testing.T) {
	cases := []struct {
		in     string
		slug   string
		digest string
		agent  string
	}{
		{"opencode-sandbox-vm-opencode-sandbox-1mjusbm3wikhb0", "opencode-sandbox-1mjusbm3wikhb0", "", ""},
		{"opencode-sandbox-vm-opencode-sandbox-1mjusbm3wikhb0-main", "opencode-sandbox-1mjusbm3wikhb0", "", "main"},
		{"opencode-sandbox/runner-myproject:xYz1234AbCdEfGh", "myproject", "xYz1234AbCdEfGh", ""},
		{"opencode-sandbox/runner-myproject:latest", "myproject", "", ""},
		{"opencode-sandbox/runner-myproject", "myproject", "", ""},
		{
			"opencode-sandbox-home-myproject-aB3cDe4fGhIjKl-xYz1234AbCdEfGh",
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
		{"opencode-sandbox-task-prefill-proj-1719432000", "prefill-proj", ""},
		{"opencode-sandbox-task-fill-proj", "fill", ""},
		{"opencode-sandbox-task-prefill", "prefill", ""},
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
		{"opencode-sandbox-other-myslug", "", ""},
		{"opencode-sandbox-vm-", "", ""},
		{"opencode-sandbox-home-", "", ""},
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
		{"hash at hyphen position 5", "opencode-sandbox-1mjusbm3wikhb0", 17},
		{"hash followed by branch", "opencode-sandbox-1mjusbm3wikhb0-main", 17},
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
		{"opencode-sandbox/runner-myproject:xYz1234AbCdEfGh", "myproject", "xYz1234AbCdEfGh", ""},
		{"opencode-sandbox/runner-myproject:latest", "myproject", "", ""},
		{"opencode-sandbox/runner-myproject:", "myproject", "", ""},
		{"opencode-sandbox/runner-myproject", "myproject", "", ""},
		{"opencode-sandbox/runner-my-project-name:xYz1234AbCdEfGh", "my-project-name", "xYz1234AbCdEfGh", ""},
		{"opencode-sandbox/runner-myproject:sha256:abc123", "myproject:sha256", "abc123", ""},
		{"opencode-sandbox/runner-myproject:opencode-latest", "myproject", "", "opencode"},
		{"opencode-sandbox/runner-myproject:pi-latest", "myproject", "", "pi"},
		{"opencode-sandbox/runner-myproject:claude-code-latest", "myproject", "", "claude-code"},
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
		{"opencode-sandbox-vm-opencode-sandbox-1mjusbm3wikhb0", "opencode-sandbox-1mjusbm3wikhb0", ""},
		{"opencode-sandbox-vm-opencode-sandbox-1mjusbm3wikhb0-main", "opencode-sandbox-1mjusbm3wikhb0", "main"},
		{"opencode-sandbox-vm-my-project-1mjusbm3wikhb0-develop", "my-project-1mjusbm3wikhb0", "develop"},
		{"opencode-sandbox-vm-projectname-main", "projectname-main", ""},
		{"opencode-sandbox-vm-myproject-abc1234567890", "myproject-abc1234567890", ""},
		{"opencode-sandbox-vm-noHash", "noHash", ""},
		{"opencode-sandbox-home-test", "", ""},
		{"opencode-sandbox-vm-projectname-aB3cDe4fGhIjKl", "projectname-aB3cDe4fGhIjKl", ""},
		{"opencode-sandbox-vm-projectname-aB3cDe4fGhIjKl-feature", "projectname-aB3cDe4fGhIjKl-feature", ""},
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
			"opencode-sandbox-home-myproject-aB3cDe4fGhIjKl-xYz1234AbCdEfGh",
			"myproject-aB3cDe4fGhIjKl",
			"xYz1234AbCdEfGh",
		},
		{"opencode-sandbox-home-myproject-abc1234567890", "myproject", "abc1234567890"},
		{"opencode-sandbox-home-abc-def-gh", "abc-def", "gh"},
		{"opencode-sandbox-vm-something", "", ""},
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
		{"opencode-sandbox-home-myproject-1mjusbm3wikhb0-20260812T123456", "myproject-1mjusbm3wikhb0"},
		{"opencode-sandbox-home-projectname-20260101T000000", "projectname"},
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
		{"opencode-sandbox-vm-opencode-sandbox-1mjusbm3wikhb0", "opencode-sandbox-1mjusbm3wikhb0", ""},
		{"opencode-sandbox/runner-myproject:xYz1234AbCdEfGh", "myproject", "xYz1234AbCdEfGh"},
		{
			"opencode-sandbox-home-myproject-aB3cDe4fGhIjKl-xYz1234AbCdEfGh",
			"myproject-aB3cDe4fGhIjKl",
			"xYz1234AbCdEfGh",
		},
		{"opencode-sandbox-task-prefill-proj-1719432000", "prefill-proj", ""},
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
		{"opencode-sandbox-vm-myproject-1mjusbm3wikhb0", "myproject-1mjusbm3wikhb0", ""},
		{"opencode-sandbox-vm-myproject-1mjusbm3wikhb0-opencode", "myproject-1mjusbm3wikhb0", "opencode"},
		{"opencode-sandbox-vm-myproject-1mjusbm3wikhb0-pi", "myproject-1mjusbm3wikhb0", "pi"},
		{"opencode-sandbox-vm-projectname-main", "projectname-main", ""},
		{"opencode-sandbox-vm-noHash", "noHash", ""},
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
			"opencode-sandbox-home-myproject-1mjusbm3wikhb0-opencode-20260812T123456",
			"myproject-1mjusbm3wikhb0",
			"opencode",
		},
		{"opencode-sandbox-home-myproject-1mjusbm3wikhb0-pi-20260812T123456", "myproject-1mjusbm3wikhb0", "pi"},
		// Legacy new-format: home-<slug>-<ts> (segment before ts is the slug hash).
		{"opencode-sandbox-home-myproject-1mjusbm3wikhb0-20260812T123456", "myproject-1mjusbm3wikhb0", ""},
		// Legacy digest format: home-<slug>-<digest>.
		{"opencode-sandbox-home-myproject-1mjusbm3wikhb0-xYz1234AbCdEfGh", "myproject-1mjusbm3wikhb0", ""},
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
