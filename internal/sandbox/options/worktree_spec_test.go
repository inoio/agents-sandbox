package options

import "testing"

func TestResolveWorktreeSpecNameOnly(t *testing.T) {
	got, err := ResolveWorktreeSpec("bugfix-hello-world")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name != "bugfix-hello-world" || got.Base != "" {
		t.Errorf("unexpected spec: %+v", got)
	}
}

func TestResolveWorktreeSpecNameAndBase(t *testing.T) {
	got, err := ResolveWorktreeSpec("bugfix-hello-world:main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name != "bugfix-hello-world" || got.Base != "main" {
		t.Errorf("unexpected spec: %+v", got)
	}
}

func TestResolveWorktreeSpecEmpty(t *testing.T) {
	got, err := ResolveWorktreeSpec("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name != "" || got.Base != "" {
		t.Errorf("expected zero spec, got %+v", got)
	}
}

func TestResolveWorktreeSpecRejectsNonSlugName(t *testing.T) {
	for _, in := range []string{"feature/foo", "Feature bar", "a--b", "-lead", "trail-", ":main"} {
		if _, err := ResolveWorktreeSpec(in); err == nil {
			t.Errorf("expected error for %q", in)
		}
	}
}

func TestSlugifyMatchesDaemonNaming(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"bugfix/exit-zero", "bugfix-exit-zero"},
		{"bugfix/reuse-branch", "bugfix-reuse-branch"},
		{"Feature/My-Topic", "feature-my-topic"},
		{"  spaces  ", "spaces"},
		{"/leading/dash", "leading-dash"},
	}
	for _, c := range cases {
		if got := slugify(c.in); got != c.want {
			t.Errorf("slugify(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
