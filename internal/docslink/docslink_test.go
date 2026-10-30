package docslink

import "testing"

func TestSectionURLComposesPageAndAnchor(t *testing.T) {
	got := SectionURL("troubleshooting", "docker-not-running")
	want := BaseURL + "/troubleshooting.html#docker-not-running"
	if got != want {
		t.Errorf("SectionURL() = %q, want %q", got, want)
	}
}

func TestSectionURLUsesTheGivenPage(t *testing.T) {
	got := SectionURL("runner-image", "the-base-image-contract")
	want := BaseURL + "/runner-image.html#the-base-image-contract"
	if got != want {
		t.Errorf("SectionURL() = %q, want %q", got, want)
	}
}
