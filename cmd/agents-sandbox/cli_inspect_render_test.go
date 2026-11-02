package main

import (
	"strings"
	"testing"

	"github.com/inoio/agents-sandbox/internal/humanize"
	"github.com/inoio/agents-sandbox/internal/sandbox/image"
	"github.com/inoio/agents-sandbox/internal/sandbox/storage"
	sandbox "github.com/inoio/agents-sandbox/internal/sandbox/vm"
	"github.com/inoio/agents-sandbox/internal/termio"
)

// TestPrintStorageVerboseBlankLineLayout pins the leading blank line and the
// blank line before each category: removing either loses the paragraph break.
func TestPrintStorageVerboseBlankLineLayout(t *testing.T) {
	ui := &termio.Mock{}
	count := uint64(1)
	report := &storage.Report{Categories: []storage.Category{
		{Name: "images", Totals: storage.Totals{Count: &count}},
		{Name: "volumes", Totals: storage.Totals{Count: &count}},
	}}
	printStorageVerbose(ui, report)

	if len(ui.OutCalls) == 0 || ui.OutCalls[0] != "" {
		t.Errorf("expected a leading blank line, got %#v", ui.OutCalls)
	}
	blanks := 0
	for _, line := range ui.OutCalls {
		if line == "" {
			blanks++
		}
	}
	if blanks != 3 {
		t.Errorf("expected 3 blank lines (leading + one per category), got %d in %#v", blanks, ui.OutCalls)
	}
}

// valueAfterPrefix returns the first line's value after a key label prefix, or
// "" when no line matches.
func valueAfterPrefix(lines []string, prefix string) string {
	for _, line := range lines {
		if value, ok := strings.CutPrefix(line, prefix); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// TestPrintKeyValuesAlignsToWidestKey pins the key/value alignment: the width
// must grow across all rows (a range break would stop at the first), and the
// width is taken from the row key.
func TestPrintKeyValuesAlignsToWidestKey(t *testing.T) {
	ui := &termio.Mock{}
	printKeyValues(ui, [][2]string{
		{"a", "1"},
		{"longer", "2"},
	})
	want := []string{"a:      1", "longer:  2"}
	if len(ui.OutCalls) != len(want) {
		t.Fatalf("OutCalls = %#v, want %#v", ui.OutCalls, want)
	}
	for i := range want {
		if ui.OutCalls[i] != want[i] {
			t.Errorf("OutCalls[%d] = %q, want %q", i, ui.OutCalls[i], want[i])
		}
	}
}

// TestMibStringPinsMebibyteFactor pins the 1024*1024 conversion.
func TestMibStringPinsMebibyteFactor(t *testing.T) {
	if got, want := mibString(1), humanize.FormatBytes(1024*1024); got != want {
		t.Errorf("mibString(1) = %q, want %q", got, want)
	}
	if got := mibString(2); got != humanize.FormatBytes(2*1024*1024) {
		t.Errorf("mibString(2) = %q, want %q", got, humanize.FormatBytes(2*1024*1024))
	}
	// 1023 lies just below the MiB/GiB boundary, so a 1024->1025 factor changes
	// the rendered value.
	if got := mibString(1023); got != "1023 MiB" {
		t.Errorf("mibString(1023) = %q, want %q", got, "1023 MiB")
	}
}

// TestJoinOrDash pins both branches: an empty slice renders "-", a non-empty
// slice joins with ", ".
func TestJoinOrDash(t *testing.T) {
	if got := joinOrDash(nil); got != "-" {
		t.Errorf("joinOrDash(nil) = %q, want %q", got, "-")
	}
	if got := joinOrDash([]string{"a", "b"}); got != "a, b" {
		t.Errorf("joinOrDash([a b]) = %q, want %q", got, "a, b")
	}
}

// TestNonNilStringsReturnsInput pins that a non-nil slice is returned unchanged,
// not zeroed.
func TestNonNilStringsReturnsInput(t *testing.T) {
	got := nonNilStrings([]string{"a", "b"})
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("nonNilStrings([a b]) = %v, want [a b]", got)
	}
	if nonNilStrings(nil) == nil {
		t.Error("nonNilStrings(nil) = nil, want an empty non-nil slice")
	}
}

// TestPrintImageInspectLayerSection pins that the layer header and rows appear
// for a non-empty layer list and are absent for an empty one.
func TestPrintImageInspectLayerSection(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		ui := &termio.Mock{}
		printImageInspect(ui, image.ImageDetail{Reference: "img"})
		for _, line := range ui.OutCalls {
			if strings.HasPrefix(line, "layers (") {
				t.Errorf("unexpected layer header for an empty layer list: %q", line)
			}
		}
	})
	t.Run("one layer", func(t *testing.T) {
		ui := &termio.Mock{}
		printImageInspect(ui, image.ImageDetail{Reference: "img", Layers: []image.LayerInfo{{
			Position:       1,
			DiffID:         "sha256:diff",
			BlobDigest:     "sha256:blob",
			CompressedSize: "2 MiB",
			ErofsSize:      "1 MiB",
			MediaType:      "application/vnd.oci.image.layer.v1.tar",
		}}})
		joined := strings.Join(ui.OutCalls, "\n")
		if !strings.Contains(joined, "layers (1):") {
			t.Errorf("missing layer header: %q", joined)
		}
		want := "  1  sha256:diff  sha256:blob  2 MiB  erofs 1 MiB  application/vnd.oci.image.layer.v1.tar"
		if !strings.Contains(joined, want) {
			t.Errorf("missing exact layer row %q in:\n%s", want, joined)
		}
	})
}

// TestPrintSandboxInspectMountsAndCPUs pins the mount section's presence gating,
// the read-only marker, and the decimal CPU rendering.
func TestPrintSandboxInspectMountsAndCPUs(t *testing.T) {
	t.Run("no mounts", func(t *testing.T) {
		ui := &termio.Mock{}
		printSandboxInspect(ui, sandbox.SandboxDetail{Name: "sb", CPUs: 10, MaxCPUs: 11})
		joined := strings.Join(ui.OutCalls, "\n")
		for _, line := range ui.OutCalls {
			if strings.HasPrefix(line, "mounts (") {
				t.Errorf("unexpected mount header for an empty mount list: %q", line)
			}
		}
		if got := valueAfterPrefix(ui.OutCalls, "cpus:"); got != "10" {
			t.Errorf("cpus value = %q, want %q; all:\n%s", got, "10", joined)
		}
		if got := valueAfterPrefix(ui.OutCalls, "max cpus:"); got != "11" {
			t.Errorf("max cpus value = %q, want %q; all:\n%s", got, "11", joined)
		}
	})
	t.Run("one read-only mount", func(t *testing.T) {
		ui := &termio.Mock{}
		printSandboxInspect(ui, sandbox.SandboxDetail{
			Name:   "sb",
			Mounts: []sandbox.MountDetail{{GuestPath: "/workspace", Kind: "bind", Source: "/host", Readonly: true}},
		})
		joined := strings.Join(ui.OutCalls, "\n")
		if !strings.Contains(joined, "mounts (1):") {
			t.Errorf("missing mount header: %q", joined)
		}
		if !strings.Contains(joined, "  /workspace  bind  /host  (read-only)") {
			t.Errorf("missing read-only mount row in:\n%s", joined)
		}
	})
}

// TestFormatOptionalUintPinsDecimal pins the base-10 rendering used by the
// storage report.
func TestFormatOptionalUintPinsDecimal(t *testing.T) {
	ten := uint64(10)
	if got := formatOptionalUint(&ten); got != "10" {
		t.Errorf("formatOptionalUint(10) = %q, want %q", got, "10")
	}
	if got := formatOptionalUint(nil); got != "-" {
		t.Errorf("formatOptionalUint(nil) = %q, want %q", got, "-")
	}
}

// TestFormatOptionalBoolPinsUnknownSentinel pins the nil rendering used by the
// storage report.
func TestFormatOptionalBoolPinsUnknownSentinel(t *testing.T) {
	if got := formatOptionalBool(nil); got != "-" {
		t.Errorf("formatOptionalBool(nil) = %q, want %q", got, "-")
	}
}
