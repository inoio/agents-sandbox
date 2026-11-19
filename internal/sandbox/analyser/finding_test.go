package analyser

import "testing"

func TestEvidenceHashStableAndEvidenceSensitive(t *testing.T) {
	a := Evidence{Summary: "used api.example", Destinations: []string{"api.example", "cdn.example"}, Count: 3}
	b := Evidence{Summary: "used api.example", Destinations: []string{"cdn.example", "api.example"}, Count: 3}
	if a.hash() != b.hash() {
		t.Fatal("list order must not change the hash")
	}
	c := a
	c.Count = 4
	if a.hash() == c.hash() {
		t.Fatal("a changed count must change the hash")
	}
}

func TestEvidenceHashAnyListOrderIsStable(t *testing.T) {
	a := Evidence{
		Denied:       []string{"denied.example", "blocked.example"},
		HeaderFields: []string{"Authorization", "X-Api-Key"},
		Channel:      "proxy",
	}
	b := Evidence{
		Denied:       []string{"blocked.example", "denied.example"},
		HeaderFields: []string{"X-Api-Key", "Authorization"},
		Channel:      "proxy",
	}
	if a.hash() != b.hash() {
		t.Fatal("order of any list field must not change the hash")
	}
	c := a
	c.HeaderFields = []string{"Authorization"}
	if a.hash() == c.hash() {
		t.Fatal("a changed header-field set must change the hash")
	}
	d := a
	d.Succeeded = true
	if a.hash() == d.hash() {
		t.Fatal("a changed outcome must change the hash")
	}
}

func TestEvidenceHashDoesNotReorderTheEvidence(t *testing.T) {
	destinations := []string{"cdn.example", "api.example"}
	e := Evidence{Destinations: destinations}
	_ = e.hash()
	if destinations[0] != "cdn.example" {
		t.Fatalf("hash must not reorder the caller's slice, got %v", destinations)
	}
}

func TestWithEvidenceHashStampsTheFindingsEvidence(t *testing.T) {
	evidence := Evidence{Summary: "used api.example", Destinations: []string{"api.example"}}
	f := Finding{Evidence: evidence}.withEvidenceHash()
	if f.EvidenceHash != evidence.hash() {
		t.Fatalf("withEvidenceHash must stamp the evidence digest, got %q", f.EvidenceHash)
	}
	if (Finding{Evidence: evidence}.withEvidenceHash()).EvidenceHash != f.EvidenceHash {
		t.Fatal("the same evidence must stamp the same digest")
	}
}

func TestMoveIsOneFindingTwoWrites(t *testing.T) {
	f := Finding{
		Kind: KindConfig, Evidence: Evidence{Summary: "secret in the project config"},
		Writes: []Write{
			{Scope: ScopeProject, Secret: &SecretWrite{EnvVar: "OPENAI_API_KEY", Remove: true}},
			{
				Scope:  ScopePerSlugUser,
				Secret: &SecretWrite{EnvVar: "OPENAI_API_KEY", Hosts: []string{"api.openai.com"}},
			},
		},
	}
	if len(f.Writes) != 2 || f.Writes[0].Scope != ScopeProject || f.Writes[1].Scope != ScopePerSlugUser {
		t.Fatalf("a move must be two scoped writes, got %+v", f.Writes)
	}
}
