package configwizard

import "testing"

func TestDefaultCatalogIsWellFormed(t *testing.T) {
	seen := make(map[string]bool)
	for _, q := range DefaultCatalog() {
		if q.Kind == GroupConfirmKind {
			if q.Group == "" {
				t.Errorf("group confirm %q has no group", q.Prompt)
			}
			continue
		}
		key := QuestionKey(q)
		if key == "" {
			t.Errorf("question %q has no key path", q.Prompt)
		}
		if seen[key] {
			t.Errorf("duplicate key %q", key)
		}
		seen[key] = true
		if q.Default == "" {
			t.Errorf("question %q has no default", key)
		}
		if q.Validate != nil {
			if err := q.Validate(q.Default); err != nil {
				t.Errorf("question %q default %q fails its own validation: %v", key, q.Default, err)
			}
		}
	}
	if !seen["agent"] || !seen["network.profile"] || !seen["cpus"] {
		t.Errorf("catalog is missing expected keys: %v", seen)
	}
}
