package naming

import "testing"

func TestArtifactFor_Dispatch(t *testing.T) {
	cases := []struct {
		in     string
		slug   string
		digest string
	}{
		{"opencode-sandbox-vm-opencode-sandbox-1mjusbm3wikhb0", "opencode-sandbox-1mjusbm3wikhb0", ""},
		{"opencode-sandbox-vm-opencode-sandbox-1mjusbm3wikhb0-main", "opencode-sandbox-1mjusbm3wikhb0", "main"},
		{"opencode-sandbox/runner-myproject:xYz1234AbCdEfGh", "myproject", "xYz1234AbCdEfGh"},
		{"opencode-sandbox/runner-myproject:latest", "myproject", ""},
		{"opencode-sandbox/runner-myproject", "myproject", ""},
		{
			"opencode-sandbox-home-myproject-aB3cDe4fGhIjKl-xYz1234AbCdEfGh",
			"myproject-aB3cDe4fGhIjKl",
			"xYz1234AbCdEfGh",
		},
	}
	for _, c := range cases {
		info := ArtifactFor(c.in)
		if info.Slug != c.slug || info.Digest != c.digest {
			t.Errorf("%q: got (%q,%q) want (%q,%q)", c.in, info.Slug, info.Digest, c.slug, c.digest)
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
		{"opencode-sandbox-clone-proj-aBc1234D-1719432000", "proj-aBc1234D", ""},
		{"opencode-sandbox-clone-work-a1b2c3d4", "work", ""},
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
	}{
		{"opencode-sandbox/runner-myproject:xYz1234AbCdEfGh", "myproject", "xYz1234AbCdEfGh"},
		{"opencode-sandbox/runner-myproject:latest", "myproject", ""},
		{"opencode-sandbox/runner-myproject:", "myproject", ""},
		{"opencode-sandbox/runner-myproject", "myproject", ""},
		{"opencode-sandbox/runner-my-project-name:xYz1234AbCdEfGh", "my-project-name", "xYz1234AbCdEfGh"},
		{"opencode-sandbox/runner-myproject:sha256:abc123", "myproject:sha256", "abc123"},
		{"other-image/myproject:tag", "", ""},
	}
	for _, c := range cases {
		info := ParseImageTag(c.in)
		if info.Slug != c.wantSlug {
			t.Errorf("ParseImageTag(%q) slug = %q, want %q", c.in, info.Slug, c.wantSlug)
		}
		if info.Digest != c.wantDigest {
			t.Errorf("ParseImageTag(%q) digest = %q, want %q", c.in, info.Digest, c.wantDigest)
		}
	}
}

func TestParseVMName(t *testing.T) {
	cases := []struct {
		in         string
		wantSlug   string
		wantDigest string
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
		if info.Digest != c.wantDigest {
			t.Errorf("ParseVMName(%q) digest = %q, want %q", c.in, info.Digest, c.wantDigest)
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
		{"opencode-sandbox-home-myproject-aB3cDe4fGhIjKl-20260812T123456", "myproject-aB3cDe4fGhIjKl"},
		{"opencode-sandbox-home-projectname-20260101T000000", "projectname"},
	}
	for _, c := range cases {
		info := ParseHomeVolumeName(c.in)
		if info.Slug != c.wantSlug {
			t.Errorf("ParseHomeVolumeName(%q) slug = %q, want %q", c.in, info.Slug, c.wantSlug)
		}
	}
}

func TestParseCloneVolumeName(t *testing.T) {
	cases := []struct {
		in       string
		wantSlug string
	}{
		{"opencode-sandbox-clone-proj-aBc1234D-1719432000", "proj-aBc1234D"},
		{"opencode-sandbox-clone-work-a1b2c3d4", "work"},
		{"opencode-sandbox-clone-singlepart", "singlepart"},
		{"other-name", ""},
	}
	for _, c := range cases {
		got := ParseCloneVolumeName(c.in)
		if got != c.wantSlug {
			t.Errorf("ParseCloneVolumeName(%q) = %q, want %q", c.in, got, c.wantSlug)
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
		{"opencode-sandbox-clone-proj-aBc1234D-1719432000", "proj-aBc1234D", ""},
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

func TestConstants(t *testing.T) {
	if Prefix != "opencode-sandbox" {
		t.Errorf("Prefix = %q, want %q", Prefix, "opencode-sandbox")
	}
	if BaseSlug != "base" {
		t.Errorf("BaseSlug = %q, want %q", BaseSlug, "base")
	}
	if SbPrefix != "opencode-sandbox-" {
		t.Errorf("SbPrefix = %q, want %q", SbPrefix, "opencode-sandbox-")
	}
	if VmPrefix != "opencode-sandbox-vm-" {
		t.Errorf("VmPrefix = %q, want %q", VmPrefix, "opencode-sandbox-vm-")
	}
	if HomePrefix != "opencode-sandbox-home-" {
		t.Errorf("HomePrefix = %q, want %q", HomePrefix, "opencode-sandbox-home-")
	}
	if ClonePrefix != "opencode-sandbox-clone-" {
		t.Errorf("ClonePrefix = %q, want %q", ClonePrefix, "opencode-sandbox-clone-")
	}
	if TaskPrefix != "opencode-sandbox-task-" {
		t.Errorf("TaskPrefix = %q, want %q", TaskPrefix, "opencode-sandbox-task-")
	}
	if ImagePrefix != "opencode-sandbox/runner-" {
		t.Errorf("ImagePrefix = %q, want %q", ImagePrefix, "opencode-sandbox/runner-")
	}
	if BaseImagePrefix != "opencode-sandbox/runner-base" {
		t.Errorf("BaseImagePrefix = %q, want %q", BaseImagePrefix, "opencode-sandbox/runner-base")
	}
	if BaseTag != "opencode-sandbox/runner-base:latest" {
		t.Errorf("BaseTag = %q, want %q", BaseTag, "opencode-sandbox/runner-base:latest")
	}
	if DindBaseTag != "opencode-sandbox/runner-base-dind:latest" {
		t.Errorf("DindBaseTag = %q, want %q", DindBaseTag, "opencode-sandbox/runner-base-dind:latest")
	}
}
