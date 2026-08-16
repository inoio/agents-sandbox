package image

import "testing"

func TestParseImageVersion(t *testing.T) {
	got := parseImageVersion(map[string]string{
		OpenCodeVersionLabel: "1.2.3",
		"other":              "x",
	})
	if got != "1.2.3" {
		t.Errorf("parseImageVersion = %q, want %q", got, "1.2.3")
	}
}

func TestParseImageVersionMissing(t *testing.T) {
	if got := parseImageVersion(nil); got != "" {
		t.Errorf("parseImageVersion(nil) = %q, want empty", got)
	}
}
