package naming

import (
	"slices"
	"testing"
)

// fuzzAgents are the real agent names, so the fuzzer explores the artifact-name
// space the launcher emits rather than adversarial agent shapes (an agent whose
// own last segment were 14 alphanumerics would make the hash anchor inherently
// ambiguous, and naming cannot know agent names).
var fuzzAgents = []string{"opencode", "opencode2", "pi", "claude-code"}

// FuzzArtifactNameRoundTrip checks that ParseVMName and ParseHomeVolumeName
// recover the slug and agent from the names the launcher writes, including a
// folder whose own last segment is hash-shaped (which a leftmost anchor would
// mistake for the slug hash). The image-reference equivalent is
// FuzzRunnerImageRefRoundTrip.
func FuzzArtifactNameRoundTrip(f *testing.F) {
	f.Add("myproj", "1mjusbm3wikhb0", "opencode")
	f.Add("go-1234567890abcd", "1mjusbm3wikhb0", "claude-code")
	f.Add("a-b-c-d-e-f", "0123456789abcd", "opencode2")
	f.Fuzz(func(t *testing.T, folder, hash, agent string) {
		if !validArtifactPart(folder) || !isBase36Hash(hash) || !slices.Contains(fuzzAgents, agent) {
			t.Skip()
		}
		slug := folder + "-" + hash

		vm := ParseVMName(projectVMName(slug, agent))
		if vm.Slug != slug || vm.Agent != agent || vm.Task {
			t.Fatalf("ParseVMName(projectVMName(%q, %q)) = %+v", slug, agent, vm)
		}
		task := ParseVMName(TaskSandboxName(slug, agent, 1729000000000000000))
		if task.Slug != slug || task.Agent != agent || !task.Task {
			t.Fatalf("ParseVMName(TaskSandboxName(%q, %q)) = %+v", slug, agent, task)
		}
		volume := ParseHomeVolumeName(HomeVolumeName(slug, agent, "20260812T123456"))
		if volume.Slug != slug || volume.Agent != agent {
			t.Fatalf("ParseHomeVolumeName(HomeVolumeName(%q, %q)) = %+v", slug, agent, volume)
		}
		legacyVolume := ParseHomeVolumeName(legacyHomeVolumeName(slug, agent, "20260812T123456"))
		if legacyVolume.Slug != slug || legacyVolume.Agent != agent {
			t.Fatalf("ParseHomeVolumeName(legacyHomeVolumeName(%q, %q)) = %+v", slug, agent, legacyVolume)
		}
	})
}

// FuzzRunnerImageRefRoundTrip checks that the launcher can read back the image
// references it writes: ParseImageTag must recover the slug and agent from a
// RunnerImageRef. Naming feeds sandbox identity, so a mis-parse silently
// targets the wrong artifact.
func FuzzRunnerImageRefRoundTrip(f *testing.F) {
	f.Add("myproject", "opencode")
	f.Add("a", "pi")
	f.Add("agents-sandbox", "opencode2")
	f.Add("my-project-2", "claude-code")
	f.Add("x--y", "a-b")

	f.Fuzz(func(t *testing.T, slug, agent string) {
		if !validArtifactPart(slug) || !validArtifactPart(agent) {
			t.Skip()
		}
		ref := RunnerImageRef(slug, agent)
		info := ParseImageTag(ref)
		if info.Slug != slug {
			t.Fatalf(
				"ParseImageTag(RunnerImageRef(%q, %q)) slug = %q, want %q (ref %q)",
				slug,
				agent,
				info.Slug,
				slug,
				ref,
			)
		}
		if info.Agent != agent {
			t.Fatalf(
				"ParseImageTag(RunnerImageRef(%q, %q)) agent = %q, want %q (ref %q)",
				slug,
				agent,
				info.Agent,
				agent,
				ref,
			)
		}
	})
}

// FuzzContentImageRefRoundTrip checks that a content-addressed runner
// reference round-trips through ParseImageTag: the content id must come back on
// ContentID (not folded into the agent or read as a legacy Digest), since a
// mis-parse silently targets the wrong cached image.
func FuzzContentImageRefRoundTrip(f *testing.F) {
	f.Add("myproject", "opencode", "9f2c1a7b4d6e0f31")
	f.Add("x", "claude-code", "0000000000000000")

	f.Fuzz(func(t *testing.T, slug, agent, contentID string) {
		if !validArtifactPart(slug) || !validArtifactPart(agent) || contentID == "" || !validContentID(contentID) {
			t.Skip()
		}
		info := ParseImageTag(ContentImageRef(slug, agent, contentID))
		if info.Slug != slug || info.Agent != agent || info.ContentID != contentID {
			t.Fatalf("ParseImageTag(ContentImageRef(%q,%q,%q)) = %+v", slug, agent, contentID, info)
		}
	})
}

// validContentID constrains fuzzed content ids to the lowercase-hex prefix
// (1..16 chars) that the launcher derives from an image ID.
func validContentID(s string) bool {
	if s == "" || len(s) > 16 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// validArtifactPart constrains fuzzed inputs to the shape the launcher itself
// generates (lowercase alphanumerics joined by interior dashes), so the fuzzer
// explores that space rather than producing inputs the naming scheme never
// emits.
func validArtifactPart(s string) bool {
	if s == "" || len(s) > 40 {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case r == '-' && i > 0 && i < len(s)-1:
		default:
			return false
		}
	}
	return true
}
