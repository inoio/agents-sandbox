package naming

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// fixtureSlug is a realistic project slug: a folder name joined to a 14-char
// base36 hash, the only shape findHashSuffix recognises.
const fixtureSlug = "myproj-1mjusbm3wikhb0"

func TestTaskSandboxNameIsPrefixFree(t *testing.T) {
	require.Equal(t, "myproj-1mjusbm3wikhb0-opencode-1729000000000000000",
		TaskSandboxName(fixtureSlug, "opencode", 1729000000000000000))
}

func TestHomeVolumeNameIsPrefixFree(t *testing.T) {
	require.Equal(t, "myproj-1mjusbm3wikhb0-opencode-20260812T123456",
		HomeVolumeName(fixtureSlug, "opencode", "20260812T123456"))
}

func TestRunnerImageRefIsPrefixFree(t *testing.T) {
	require.Equal(t, "myproj-1mjusbm3wikhb0:opencode-latest", RunnerImageRef(fixtureSlug, "opencode"))
	require.Equal(t, "agents-sandbox/runner-myproj-1mjusbm3wikhb0:opencode-latest",
		DockerImageRef(fixtureSlug, "opencode"))
}

func TestParseVMNameBothSchemes(t *testing.T) {
	cases := []struct {
		name string
		want ArtifactInfo
	}{
		{"myproj-1mjusbm3wikhb0-opencode", ArtifactInfo{Slug: fixtureSlug, Agent: "opencode"}},
		{"agents-sandbox-vm-myproj-1mjusbm3wikhb0-opencode", ArtifactInfo{Slug: fixtureSlug, Agent: "opencode"}},
		{
			"myproj-1mjusbm3wikhb0-opencode-1729000000000000000",
			ArtifactInfo{Slug: fixtureSlug, Agent: "opencode", Task: true},
		},
		{"myproj-1mjusbm3wikhb0-claude-code", ArtifactInfo{Slug: fixtureSlug, Agent: "claude-code"}},
		{
			"myproj-1mjusbm3wikhb0-claude-code-1729000000000000000",
			ArtifactInfo{Slug: fixtureSlug, Agent: "claude-code", Task: true},
		},
		{
			"agents-sandbox-task-myproj-1mjusbm3wikhb0-opencode-1729000000000000000",
			ArtifactInfo{Slug: fixtureSlug, Agent: "opencode", Task: true},
		},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, ParseVMName(tc.name), tc.name)
	}
}

// TestParseNamesWithHashShapedFolderSegment pins the anchor against a folder
// whose own last segment is 14 alphanumerics: the real slug hash is the
// rightmost segment-bounded run, so the folder segment must not be taken as the
// hash (which would fold the hash and agent into the slug).
func TestParseNamesWithHashShapedFolderSegment(t *testing.T) {
	slug := "go-1234567890abcd-1mjusbm3wikhb0"
	require.Equal(t, ArtifactInfo{Slug: slug, Agent: "opencode"},
		ParseVMName(projectVMName(slug, "opencode")))
	require.Equal(t, ArtifactInfo{Slug: slug, Agent: "opencode"},
		ParseVMName(legacyProjectVMName(slug, "opencode")))
	require.Equal(t, ArtifactInfo{Slug: slug, Agent: "claude-code", Task: true},
		ParseVMName(TaskSandboxName(slug, "claude-code", 1729000000000000000)))
	require.Equal(t, ArtifactInfo{Slug: slug, Agent: "opencode"},
		ParseHomeVolumeName(HomeVolumeName(slug, "opencode", "20260812T123456")))
	require.Equal(t, ArtifactInfo{Slug: slug, Agent: "opencode"},
		ParseHomeVolumeName(legacyHomeVolumeName(slug, "opencode", "20260812T123456")))
}

// TestParseNamesWithLegacyPrefixFolder pins the prefix-collision fix: a
// prefix-free artifact whose folder begins with a legacy prefix (a folder
// literally named "agents-sandbox-vm"/"-task"/"-home") must parse prefix-free,
// not have the legacy prefix stripped.
func TestParseNamesWithLegacyPrefixFolder(t *testing.T) {
	taskFolder := "agents-sandbox-task-1mjusbm3wikhb0"
	require.Equal(t, ArtifactInfo{Slug: taskFolder, Agent: "opencode"},
		ParseVMName(projectVMName(taskFolder, "opencode")))

	vmFolder := "agents-sandbox-vm-1mjusbm3wikhb0"
	require.Equal(t, ArtifactInfo{Slug: vmFolder, Agent: "opencode"},
		ParseVMName(projectVMName(vmFolder, "opencode")))

	homeFolder := "agents-sandbox-home-1mjusbm3wikhb0"
	require.Equal(t, ArtifactInfo{Slug: homeFolder, Agent: "opencode"},
		ParseVMName(projectVMName(homeFolder, "opencode")))
	require.Equal(t, ArtifactInfo{Slug: homeFolder, Agent: "opencode"},
		ParseHomeVolumeName(HomeVolumeName(homeFolder, "opencode", "20260812T123456")))

	// A genuine legacy name still parses.
	require.Equal(t, ArtifactInfo{Slug: fixtureSlug, Agent: "opencode"},
		ParseVMName(legacyProjectVMName(fixtureSlug, "opencode")))
}

func TestParseHomeVolumeNameHyphenatedAgent(t *testing.T) {
	require.Equal(t, ArtifactInfo{Slug: fixtureSlug, Agent: "claude-code"},
		ParseHomeVolumeName("myproj-1mjusbm3wikhb0-claude-code-20260812T123456"))
	require.Equal(t, ArtifactInfo{Slug: fixtureSlug, Agent: "claude-code"},
		ParseHomeVolumeName("agents-sandbox-home-myproj-1mjusbm3wikhb0-claude-code-20260812T123456"))
}

// TestParseHomeVolumeNameLegacyDigest pins the legacy digest format when both
// the project hash and the digest are lowercase 14-char base36 runs, the only
// shape findHashSuffix cannot tell apart by segment shape: it anchors on the
// digest, and the parser must step back to the project hash so prune can still
// match the saved volume to its project.
func TestParseHomeVolumeNameLegacyDigest(t *testing.T) {
	const digest = "4e8oeqohbmalok"
	require.Equal(t, ArtifactInfo{Slug: fixtureSlug, Digest: digest},
		ParseHomeVolumeName("agents-sandbox-home-myproj-1mjusbm3wikhb0-"+digest))

	// A legacy digest name still parses when the folder's own last segment is
	// hash-shaped.
	hashShapedFolder := "go-1234567890abcd-1mjusbm3wikhb0"
	require.Equal(t, ArtifactInfo{Slug: hashShapedFolder, Digest: digest},
		ParseHomeVolumeName("agents-sandbox-home-go-1234567890abcd-1mjusbm3wikhb0-"+digest))
}

func TestParseImageTagBothSchemes(t *testing.T) {
	require.Equal(t, ArtifactInfo{Slug: fixtureSlug, Agent: "opencode"},
		ParseImageTag("myproj-1mjusbm3wikhb0:opencode-latest"))
	require.Equal(t, ArtifactInfo{Slug: fixtureSlug, Agent: "opencode"},
		ParseImageTag("agents-sandbox/runner-myproj-1mjusbm3wikhb0:opencode-latest"))
	require.Equal(t, ArtifactInfo{}, ParseImageTag("docker.io/library/foo:1"))
	// A foreign reference without a tag is not ours either.
	require.Equal(t, ArtifactInfo{}, ParseImageTag("docker.io/library/foo"))
}

// TestIsRunnerImageRef pins ownership: a reference is ours only when its parsed
// slug embeds the 14-character base36 hash every launcher-built slug carries,
// under either reference scheme. A single-segment reference without that hash
// (a manually loaded image) and a repository reference are both foreign.
func TestIsRunnerImageRef(t *testing.T) {
	owned := []string{
		"myproj-1mjusbm3wikhb0:opencode-latest",
		"myproj-1mjusbm3wikhb0:opencode.9f2c1a7b4d6e0f31",
		"agents-sandbox/runner-myproj-1mjusbm3wikhb0:opencode-latest",
		"agents-sandbox/runner-myproj-1mjusbm3wikhb0:opencode.9f2c1a7b4d6e0f31",
	}
	for _, ref := range owned {
		require.True(t, IsRunnerImageRef(ref), ref)
	}
	foreign := []string{
		"",
		"manualimage",
		"manualimage:latest",
		"manualimage:opencode-latest",
		"agents-sandbox/runner-manualimage:latest",
		"docker.io/library/alpine:latest",
		// A hyphenated single segment whose tail is not a slug hash.
		"manual-image:opencode-latest",
		// Fourteen base36 characters but no hyphen-delimited hash segment: the
		// whole name is the slug, so there is no hash to embed.
		"abcdefghijklmn",
	}
	for _, ref := range foreign {
		require.False(t, IsRunnerImageRef(ref), ref)
	}
}

// TestImageIdentityRef pins the structured identity's rendered forms: a content
// id yields the content-addressed reference, no content id the prefix-free name
// reference, and an incomplete slug/agent pair renders nothing under either
// spelling (a runner image is always a per-agent image).
func TestImageIdentityRef(t *testing.T) {
	identity := ImageIdentity{Slug: fixtureSlug, Agent: "opencode"}
	require.Equal(t, "myproj-1mjusbm3wikhb0:opencode-latest", identity.NameRef())
	require.Equal(t, "myproj-1mjusbm3wikhb0:opencode-latest", identity.Ref())

	withContent := identity
	withContent.ContentID = "9f2c1a7b4d6e0f31"
	require.Equal(t, "myproj-1mjusbm3wikhb0:opencode.9f2c1a7b4d6e0f31", withContent.Ref())
	// The name reference ignores the content id: it is the migration candidate.
	require.Equal(t, "myproj-1mjusbm3wikhb0:opencode-latest", withContent.NameRef())

	incomplete := []ImageIdentity{
		{},
		{Slug: fixtureSlug},
		{Agent: "opencode"},
		{Slug: fixtureSlug, ContentID: "9f2c1a7b4d6e0f31"},
		{Agent: "opencode", ContentID: "9f2c1a7b4d6e0f31"},
	}
	for _, i := range incomplete {
		require.Empty(t, i.Ref(), i)
		require.Empty(t, i.NameRef(), i)
	}
}

func TestContentImageRefRoundTrips(t *testing.T) {
	ref := ContentImageRef("myproj-1abc1234defg5", "claude-code", "9f2c1a7b4d6e0f31")
	require.Equal(t, "myproj-1abc1234defg5:claude-code.9f2c1a7b4d6e0f31", ref)
	require.Equal(t, ArtifactInfo{Slug: "myproj-1abc1234defg5", Agent: "claude-code", ContentID: "9f2c1a7b4d6e0f31"},
		ParseImageTag(ref))
}

func TestParseImageTagContentVsNameVsDigest(t *testing.T) {
	cases := []struct {
		ref  string
		want ArtifactInfo
	}{
		{
			"myproj-1abc1234defg5:opencode-latest",
			ArtifactInfo{Slug: "myproj-1abc1234defg5", Agent: "opencode"},
		},
		{
			"myproj-1abc1234defg5:opencode.9f2c1a7b4d6e0f31",
			ArtifactInfo{Slug: "myproj-1abc1234defg5", Agent: "opencode", ContentID: "9f2c1a7b4d6e0f31"},
		},
		{
			"myproj-1abc1234defg5:opencode.0000000000000000",
			ArtifactInfo{Slug: "myproj-1abc1234defg5", Agent: "opencode", ContentID: "0000000000000000"},
		},
		{
			"agents-sandbox/runner-myproj-1abc1234defg5:opencode.9f2c1a7b4d6e0f31",
			ArtifactInfo{Slug: "myproj-1abc1234defg5", Agent: "opencode", ContentID: "9f2c1a7b4d6e0f31"},
		},
		{
			"myproj-1abc1234defg5:xYz1234AbCdEfGh",
			ArtifactInfo{Slug: "myproj-1abc1234defg5", Digest: "xYz1234AbCdEfGh"},
		},
		// A trailing dot with no content id is not the content shape: the empty
		// id must not set Agent with an empty ContentID, so it stays a Digest.
		{
			"myproj-1abc1234defg5:opencode.",
			ArtifactInfo{Slug: "myproj-1abc1234defg5", Digest: "opencode."},
		},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, ParseImageTag(tc.ref), tc.ref)
	}
}

// TestParseHomeVolumeNameEdgeCases pins the small branches of the home-volume
// parser: an empty input, a hash with no tail, a hash-anchored prefix-free
// volume whose folder begins with a legacy VM prefix, and a legacy prefixed
// name carrying no hash at all.
func TestParseHomeVolumeNameEdgeCases(t *testing.T) {
	require.Equal(t, ArtifactInfo{}, ParseHomeVolumeName(""))
	require.Equal(t, ArtifactInfo{Slug: fixtureSlug}, ParseHomeVolumeName(fixtureSlug))
	require.Equal(t, ArtifactInfo{Slug: "agents-sandbox-vm-1mjusbm3wikhb0", Agent: "opencode"},
		ParseHomeVolumeName("agents-sandbox-vm-1mjusbm3wikhb0-opencode-20260812T123456"))
	require.Equal(t, ArtifactInfo{Slug: "single"}, ParseHomeVolumeName("agents-sandbox-home-single"))
	// A prefix-free name with no embedded hash is not ours.
	require.Equal(t, ArtifactInfo{}, ParseHomeVolumeName("foo-bar"))
}

func TestAllDigits(t *testing.T) {
	require.False(t, allDigits(""))
	require.False(t, allDigits("12a"))
	require.True(t, allDigits("12"))
	// A character below '0' must not pass as a digit.
	require.False(t, allDigits("1/2"))
	require.False(t, allDigits("/"))
}

func TestLooksLikeProjectSlug(t *testing.T) {
	require.False(t, looksLikeProjectSlug(""))
	require.False(t, looksLikeProjectSlug("a/b"))
	require.True(t, looksLikeProjectSlug("foo"))
}

// TestFindHashSuffixBoundaries pins every bound of the right-to-left scan: the
// loop's lower limit, the '-'-preceded guard, the 14-char window, and both
// edges of the base36 predicate. Each case distinguishes the original from a
// loop/boundary or operand-removal mutation.
func TestFindHashSuffixBoundaries(t *testing.T) {
	cases := []struct {
		name string
		want int
	}{
		// The hash is the whole name body at index 1 (loop lower bound 1).
		{"-abcdefghijklmn", 1},
		// Index 1 is a valid run, but not preceded by '-', so it must not anchor.
		{"0abcdefghijklmn", -1},
		// End-anchored run after a leading segment; the 14-char window is exact.
		{"a-abcdefghijklmn", 2},
		{"myproj-1mjusbm3wikhb0", 7},
		// A 14-char run containing '{' (outside a-z) is not a hash.
		{"x-abcdefghijklm{", -1},
		// A run ending in 'z' or '9' is still base36.
		{"x-abcdefghijklmz", 2},
		{"x-abcdefghijklm9", 2},
		// No run at all.
		{"short", -1},
		{"", -1},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, findHashSuffix(tc.name), tc.name)
	}
}

func TestIsBase36HashBoundaries(t *testing.T) {
	require.False(t, isBase36Hash(""))
	require.False(t, isBase36Hash("abc"))
	require.False(t, isBase36Hash("abcdefghijklmA"))
	require.False(t, isBase36Hash("abcdefghijklm{"))
	require.False(t, isBase36Hash("abcdefghijklm/"))
	require.True(t, isBase36Hash("abcdefghijklmz"))
	require.True(t, isBase36Hash("abcdefghijklm0"))
	require.True(t, isBase36Hash("1mjusbm3wikhb0"))
}

func TestIsHomeTimestampBoundaries(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"20260812T123456", true},
		{"20260101T120009", true},
		{"30000000T000000", true},
		// s[0] outside '2'..'3'.
		{"40000000T123456", false},
		{"10000000T123456", false},
		// Non-digit in the time/date body.
		{"20260101T12/000", false},
		{"20260101T12:000", false},
		{"20260101X120000", false},
		// Wrong length.
		{"20260101T12345", false},
		{"20260101T1234567", false},
		{"", false},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, isHomeTimestamp(tc.name), tc.name)
	}
}

// TestParseImageRemainder pins the slug/agent/digest split and the foreign-ref
// rejection on both the colon and no-colon paths, for the prefixed and
// prefix-free forms.
func TestParseImageRemainder(t *testing.T) {
	require.Equal(t, ArtifactInfo{}, parseImageRemainder("docker.io/library/foo", false))
	require.Equal(t, ArtifactInfo{}, parseImageRemainder("docker.io/library/foo:1", false))
	require.Equal(t, ArtifactInfo{Slug: "slug", Agent: "opencode"},
		parseImageRemainder("slug:opencode-latest", false))
	require.Equal(t, ArtifactInfo{Slug: "slug"}, parseImageRemainder("slug:", false))
	require.Equal(t, ArtifactInfo{Slug: "slug"}, parseImageRemainder("slug:latest", false))
	require.Equal(t, ArtifactInfo{Slug: "slug", Digest: "deadbeef"},
		parseImageRemainder("slug:deadbeef", false))
	require.Equal(t, ArtifactInfo{Slug: "anything"}, parseImageRemainder("anything", true))
	require.Equal(t, ArtifactInfo{Slug: "docker.io/foo"}, parseImageRemainder("docker.io/foo", true))
	// A prefix-free body with no tag is accepted as a bare slug...
	require.Equal(t, ArtifactInfo{Slug: "foo"}, parseImageRemainder("foo", false))
	// ...while a prefixed body keeps a slash-containing slug and its digest tag.
	require.Equal(t, ArtifactInfo{Slug: "docker.io/foo", Digest: "tag"},
		parseImageRemainder("docker.io/foo:tag", true))
}

// TestParseSandboxBodyBranches pins the anchored split of a sandbox body: the
// empty tail, a single-segment tail (agent), a single all-digit tail (still the
// agent, not a task), and an agent plus a nanos tail (task).
func TestParseSandboxBodyBranches(t *testing.T) {
	cases := []struct {
		remainder string
		want      ArtifactInfo
	}{
		{fixtureSlug, ArtifactInfo{Slug: fixtureSlug}},
		{fixtureSlug + "-x", ArtifactInfo{Slug: fixtureSlug, Agent: "x"}},
		{fixtureSlug + "-123", ArtifactInfo{Slug: fixtureSlug, Agent: "123"}},
		{
			fixtureSlug + "-opencode-1729000000000000000",
			ArtifactInfo{Slug: fixtureSlug, Agent: "opencode", Task: true},
		},
	}
	for _, tc := range cases {
		got, anchored := parseSandboxBody(tc.remainder, false)
		if !anchored {
			t.Errorf("parseSandboxBody(%q) anchored = false, want true", tc.remainder)
		}
		if got != tc.want {
			t.Errorf("parseSandboxBody(%q) = %+v, want %+v", tc.remainder, got, tc.want)
		}
	}
}

// TestLegacyDigestNameBranches pins the two-hash legacy digest recognition and
// its guards: an inner hash run must exist and reach the end of the inner part.
func TestLegacyDigestNameBranches(t *testing.T) {
	cases := []struct {
		name      string
		remainder string
		hashStart int
		wantSlug  string
		wantDig   string
		wantOK    bool
	}{
		{
			// Inner hash at index 1 ending the inner part: recognised.
			"inner hash at end",
			"-abcdefghijklmn-abcdefghijklmn",
			16,
			"-abcdefghijklmn",
			"abcdefghijklmn",
			true,
		},
		{
			// Inner hash at index 1 but not at the inner end: rejected.
			"inner hash not at end",
			"-abcdefghijklmn-X-abcdefghijklmn",
			18,
			"",
			"",
			false,
		},
		{
			// No inner hash and a 13-char inner part: rejected without slicing.
			"no inner hash",
			"aaaaaaaaaaaaa-abcdefghijklmn",
			14,
			"",
			"",
			false,
		},
	}
	for _, tc := range cases {
		slug, digest, ok := legacyDigestName(tc.remainder, tc.hashStart)
		if ok != tc.wantOK || slug != tc.wantSlug || digest != tc.wantDig {
			t.Errorf("%s: legacyDigestName(%q, %d) = (%q, %q, %v), want (%q, %q, %v)",
				tc.name, tc.remainder, tc.hashStart, slug, digest, ok, tc.wantSlug, tc.wantDig, tc.wantOK)
		}
	}
}

// TestParseHomeVolumeNameBoundaries pins the no-hash positional split: the
// legacy timestamp tail, the optional preceding agent segment (and its
// base36-hash exclusion), and the digest fallback.
func TestParseHomeVolumeNameBoundaries(t *testing.T) {
	require.Equal(t, ArtifactInfo{Slug: "proj", Agent: "agent"},
		ParseHomeVolumeName("agents-sandbox-home-proj-agent-20260812T123456"))
	require.Equal(t, ArtifactInfo{Slug: "proj"},
		ParseHomeVolumeName("agents-sandbox-home-proj-20260812T123456"))
	require.Equal(t, ArtifactInfo{Slug: "proj", Digest: "deadbeef"},
		ParseHomeVolumeName("agents-sandbox-home-proj-deadbeef"))
	// A hash-anchored name with an empty tail.
	require.Equal(t, ArtifactInfo{Slug: fixtureSlug}, ParseHomeVolumeName(fixtureSlug))
}
