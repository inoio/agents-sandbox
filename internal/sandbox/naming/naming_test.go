package naming

import "testing"

func TestExtractProjectSlugAndDigest(t *testing.T) {
	cases := []struct {
		in     string
		slug   string
		digest string
	}{
		{"opencode-msb-vm-saife-1mjusbm3wikhb0", "saife-1mjusbm3wikhb0", ""},
		{"opencode-msb-vm-saife-1mjusbm3wikhb0-main", "saife-1mjusbm3wikhb0", "main"},
		{"opencode-msb/runner-myproject:xYz1234AbCdEfGh", "myproject", "xYz1234AbCdEfGh"},
		{"opencode-msb/runner-myproject:latest", "myproject", ""},
		{"opencode-msb/runner-myproject", "myproject", ""},
		{"opencode-msb-home-myproject-aB3cDe4fGhIjKl-xYz1234AbCdEfGh", "myproject-aB3cDe4fGhIjKl", "xYz1234AbCdEfGh"},
	}
	for _, c := range cases {
		gotSlug, gotDigest := ExtractProjectSlugAndDigest(c.in)
		if gotSlug != c.slug || gotDigest != c.digest {
			t.Errorf("%q: got (%q,%q) want (%q,%q)", c.in, gotSlug, gotDigest, c.slug, c.digest)
		}
	}
}

func TestExtractProjectSlugAndDigest_SinglePartSlugs(t *testing.T) {
	cases := []struct {
		in     string
		slug   string
		digest string
	}{
		{"opencode-msb-task-prefill-proj-1719432000", "prefill-proj", ""},
		{"opencode-msb-task-fill-proj", "fill", ""},
		{"opencode-msb-clone-proj-aBc1234D-1719432000", "proj-aBc1234D", ""},
		{"opencode-msb-clone-work-a1b2c3d4", "work", ""},
	}
	for _, c := range cases {
		gotSlug, gotDigest := ExtractProjectSlugAndDigest(c.in)
		if gotSlug != c.slug || gotDigest != c.digest {
			t.Errorf("%q: got (%q,%q) want (%q,%q)", c.in, gotSlug, gotDigest, c.slug, c.digest)
		}
	}
}

func TestExtractProjectSlugAndDigest_Unrecognized(t *testing.T) {
	cases := []struct {
		in         string
		wantSlug   string
		wantDigest string
	}{
		{"some-random-name", "", ""},
		{"", "", ""},
		{"opencode-msb-other-myslug", "", ""},
		{"opencode-msb-vm-", "", ""},
		{"opencode-msb-home-", "", ""},
	}
	for _, c := range cases {
		gotSlug, gotDigest := ExtractProjectSlugAndDigest(c.in)
		if gotSlug != c.wantSlug || gotDigest != c.wantDigest {
			t.Errorf("%q: got (%q,%q) want (%q,%q)", c.in, gotSlug, gotDigest, c.wantSlug, c.wantDigest)
		}
	}
}

func TestFindHashSuffix(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"no hash - simple name", "projectname-main", -1},
		{"hash at hyphen position 5", "saife-1mjusbm3wikhb0", 6},
		{"hash followed by branch", "saife-1mjusbm3wikhb0-main", 6},
		{"hash embedded in multi-dash slug", "my-project-1mjusbm3wikhb0-develop", 11},
		{"no hash - short string", "abc-def", -1},
	}
	for _, c := range cases {
		got := FindHashSuffix(c.in)
		if got != c.want {
			t.Errorf("FindHashSuffix(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestParseImageTag(t *testing.T) {
	cases := []struct {
		in         string
		wantSlug   string
		wantDigest string
	}{
		{"opencode-msb/runner-myproject:xYz1234AbCdEfGh", "myproject", "xYz1234AbCdEfGh"},
		{"opencode-msb/runner-myproject:latest", "myproject", ""},
		{"opencode-msb/runner-myproject:", "myproject", ""},
		{"opencode-msb/runner-myproject", "myproject", ""},
		{"opencode-msb/runner-my-project-name:xYz1234AbCdEfGh", "my-project-name", "xYz1234AbCdEfGh"},
		{"opencode-msb/runner-myproject:sha256:abc123", "myproject:sha256", "abc123"},
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
		{"opencode-msb-vm-saife-1mjusbm3wikhb0", "saife-1mjusbm3wikhb0", ""},
		{"opencode-msb-vm-saife-1mjusbm3wikhb0-main", "saife-1mjusbm3wikhb0", "main"},
		{"opencode-msb-vm-my-project-1mjusbm3wikhb0-develop", "my-project-1mjusbm3wikhb0", "develop"},
		{"opencode-msb-vm-projectname-main", "projectname-main", ""},
		{"opencode-msb-vm-myproject-abc1234567890", "myproject-abc1234567890", ""},
		{"opencode-msb-vm-noHash", "noHash", ""},
		{"opencode-msb-home-test", "", ""},
		{"opencode-msb-vm-projectname-aB3cDe4fGhIjKl", "projectname-aB3cDe4fGhIjKl", ""},
		{"opencode-msb-vm-projectname-aB3cDe4fGhIjKl-feature", "projectname-aB3cDe4fGhIjKl-feature", ""},
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
		{"opencode-msb-home-myproject-aB3cDe4fGhIjKl-xYz1234AbCdEfGh", "myproject-aB3cDe4fGhIjKl", "xYz1234AbCdEfGh"},
		{"opencode-msb-home-myproject-abc1234567890", "myproject", "abc1234567890"},
		{"opencode-msb-home-abc-def-gh", "abc-def", "gh"},
		{"opencode-msb-vm-something", "", ""},
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
		{"opencode-msb-home-myproject-aB3cDe4fGhIjKl-20260812T123456", "myproject-aB3cDe4fGhIjKl"},
		{"opencode-msb-home-projectname-20260101T000000", "projectname"},
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
		{"opencode-msb-clone-proj-aBc1234D-1719432000", "proj-aBc1234D"},
		{"opencode-msb-clone-work-a1b2c3d4", "work"},
		{"opencode-msb-clone-singlepart", "singlepart"},
		{"other-name", ""},
	}
	for _, c := range cases {
		got := ParseCloneVolumeName(c.in)
		if got != c.wantSlug {
			t.Errorf("ParseCloneVolumeName(%q) = %q, want %q", c.in, got, c.wantSlug)
		}
	}
}

func TestStaleTypeString(t *testing.T) {
	cases := []struct {
		staleType StaleType
		want      string
	}{
		{StaleTypeVM, "vm"},
		{StaleTypeVolume, "volume"},
		{StaleTypeDockerImage, "docker-image"},
		{StaleTypeMsbImage, "msb-image"},
	}
	for _, c := range cases {
		if c.staleType.String() != c.want {
			t.Errorf("StaleType(%d).String() = %q, want %q", c.staleType, c.staleType.String(), c.want)
		}
	}
}

func TestArtifactFor(t *testing.T) {
	cases := []struct {
		in         string
		wantSlug   string
		wantDigest string
	}{
		{"opencode-msb-vm-saife-1mjusbm3wikhb0", "saife-1mjusbm3wikhb0", ""},
		{"opencode-msb/runner-myproject:xYz1234AbCdEfGh", "myproject", "xYz1234AbCdEfGh"},
		{"opencode-msb-home-myproject-aB3cDe4fGhIjKl-xYz1234AbCdEfGh", "myproject-aB3cDe4fGhIjKl", "xYz1234AbCdEfGh"},
		{"opencode-msb-task-prefill-proj-1719432000", "prefill-proj", ""},
		{"opencode-msb-clone-proj-aBc1234D-1719432000", "proj-aBc1234D", ""},
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
	if Prefix != "opencode-msb" {
		t.Errorf("Prefix = %q, want %q", Prefix, "opencode-msb")
	}
	if BaseSlug != "base" {
		t.Errorf("BaseSlug = %q, want %q", BaseSlug, "base")
	}
	if SbPrefix != "opencode-msb-" {
		t.Errorf("SbPrefix = %q, want %q", SbPrefix, "opencode-msb-")
	}
	if VmPrefix != "opencode-msb-vm-" {
		t.Errorf("VmPrefix = %q, want %q", VmPrefix, "opencode-msb-vm-")
	}
	if HomePrefix != "opencode-msb-home-" {
		t.Errorf("HomePrefix = %q, want %q", HomePrefix, "opencode-msb-home-")
	}
	if ClonePrefix != "opencode-msb-clone-" {
		t.Errorf("ClonePrefix = %q, want %q", ClonePrefix, "opencode-msb-clone-")
	}
	if TaskPrefix != "opencode-msb-task-" {
		t.Errorf("TaskPrefix = %q, want %q", TaskPrefix, "opencode-msb-task-")
	}
	if ImagePrefix != "opencode-msb/runner-" {
		t.Errorf("ImagePrefix = %q, want %q", ImagePrefix, "opencode-msb/runner-")
	}
	if BaseImagePrefix != "opencode-msb/runner-base" {
		t.Errorf("BaseImagePrefix = %q, want %q", BaseImagePrefix, "opencode-msb/runner-base")
	}
	if BaseTag != "opencode-msb/runner-base:latest" {
		t.Errorf("BaseTag = %q, want %q", BaseTag, "opencode-msb/runner-base:latest")
	}
	if DindBaseTag != "opencode-msb/runner-base-dind:latest" {
		t.Errorf("DindBaseTag = %q, want %q", DindBaseTag, "opencode-msb/runner-base-dind:latest")
	}
}
