package options

import (
	"strings"
	"testing"
)

func TestParseMemoryOK_ValidGiB(t *testing.T) {
	got, ok := ParseMemoryOK("4G")
	if !ok {
		t.Fatal("expected ok for valid GiB spec")
	}
	if got != 4096 {
		t.Errorf("expected 4096, got %d", got)
	}
}

func TestParseMemoryOK_ValidMB(t *testing.T) {
	got, ok := ParseMemoryOK("512M")
	if !ok {
		t.Fatal("expected ok for valid MB spec")
	}
	if got != 512 {
		t.Errorf("expected 512, got %d", got)
	}
}

func TestParseMemoryOK_Empty(t *testing.T) {
	got, ok := ParseMemoryOK("")
	if ok {
		t.Fatal("expected not ok for empty spec")
	}
	if got != 0 {
		t.Errorf("expected 0, got %d", got)
	}
}

func TestParseMemoryOK_Bogus(t *testing.T) {
	got, ok := ParseMemoryOK("bogus")
	if ok {
		t.Fatal("expected not ok for bogus spec")
	}
	if got != 0 {
		t.Errorf("expected 0, got %d", got)
	}
}

func TestParseMemoryOK_ValidLowercase(t *testing.T) {
	got, ok := ParseMemoryOK("2g")
	if !ok {
		t.Fatal("expected ok for valid lowercase GiB spec")
	}
	if got != 2048 {
		t.Errorf("expected 2048, got %d", got)
	}
}

func TestParseMemoryOK_ValidPlainNumber(t *testing.T) {
	got, ok := ParseMemoryOK("2048")
	if !ok {
		t.Fatal("expected ok for valid plain number")
	}
	if got != 2048 {
		t.Errorf("expected 2048, got %d", got)
	}
}

func TestParseMemoryOK_TrailingLetterNotGOrM(t *testing.T) {
	got, ok := ParseMemoryOK("10X")
	if ok {
		t.Fatal("expected not ok for trailing non-G/M letter")
	}
	if got != 0 {
		t.Errorf("expected 0, got %d", got)
	}
}

func TestParseMemoryOK_Whitespace(t *testing.T) {
	got, ok := ParseMemoryOK("  4G  ")
	if !ok {
		t.Fatal("expected ok for spec with whitespace")
	}
	if got != 4096 {
		t.Errorf("expected 4096, got %d", got)
	}

	_, ok = ParseMemoryOK("   ")
	if ok {
		t.Fatal("expected not ok for whitespace-only spec")
	}
}

func TestParseMemoryOK_Negative(t *testing.T) {
	got, ok := ParseMemoryOK("-4")
	if ok {
		t.Fatal("expected not ok for negative spec")
	}
	if got != 0 {
		t.Errorf("expected 0, got %d", got)
	}
}

func TestParseMemoryOK_Overflow(t *testing.T) {
	got, ok := ParseMemoryOK("4194305G")
	if ok {
		t.Fatal("expected not ok for overflowing spec")
	}
	if got != 0 {
		t.Errorf("expected 0, got %d", got)
	}
}

func TestParseMemoryOK_PlainNumberAboveUint32(t *testing.T) {
	got, ok := ParseMemoryOK("4294967296")
	if ok {
		t.Fatal("expected not ok for a plain number above uint32")
	}
	if got != 0 {
		t.Errorf("expected 0, got %d", got)
	}
}

func TestParseMemoryOK_MegabyteSuffixAboveUint32(t *testing.T) {
	got, ok := ParseMemoryOK("4294967296M")
	if ok {
		t.Fatal("expected not ok for an M-suffixed spec above uint32")
	}
	if got != 0 {
		t.Errorf("expected 0, got %d", got)
	}
}

func TestParseMemoryOK_GiBAgainstUint64Overflow(t *testing.T) {
	// 2^54 GiB overflows the uint64 multiplication, so a bound check written as
	// a multiply-and-compare lets it through.
	got, ok := ParseMemoryOK("18014398509481984G")
	if ok {
		t.Fatal("expected not ok for a GiB spec whose multiplication overflows uint64")
	}
	if got != 0 {
		t.Errorf("expected 0, got %d", got)
	}
}

func TestParseMemoryOK_MaxUint32(t *testing.T) {
	got, ok := ParseMemoryOK("4294967295")
	if !ok {
		t.Fatal("expected ok for the largest representable MiB spec")
	}
	if got != 4294967295 {
		t.Errorf("expected 4294967295, got %d", got)
	}
}

func TestParseMemoryOK_LargestGiBSpec(t *testing.T) {
	got, ok := ParseMemoryOK("4194303G")
	if !ok {
		t.Fatal("expected ok for the largest GiB spec that fits in uint32 MiB")
	}
	if want := uint32(4194303) * 1024; got != want {
		t.Errorf("expected %d, got %d", want, got)
	}

	if _, ok := ParseMemoryOK("4194304G"); ok {
		t.Fatal("expected not ok for the first GiB spec that does not fit")
	}
}

func TestParseMemoryOK_DefaultMemoryFallsThrough(t *testing.T) {
	if ParseMemory("") != DefaultMemoryMiB {
		t.Errorf("expected default %d for empty spec", DefaultMemoryMiB)
	}
	if ParseMemory("bogus") != DefaultMemoryMiB {
		t.Errorf("expected default %d for bogus spec", DefaultMemoryMiB)
	}
	if ParseMemory("4294967296") != DefaultMemoryMiB {
		t.Errorf("expected default %d for an out-of-range spec, not a wrapped value", DefaultMemoryMiB)
	}
}

func TestResolveTmpSizeMiB_DelegatesToParseMemoryOK(t *testing.T) {
	if got := ResolveTmpSizeMiB("", DefaultMemoryMiB); got != DefaultTmpSizeCapMiB {
		t.Errorf("expected cap %d for empty, got %d", DefaultTmpSizeCapMiB, got)
	}
	if got := ResolveTmpSizeMiB("bogus", DefaultMemoryMiB); got != DefaultTmpSizeCapMiB {
		t.Errorf("expected cap %d for bogus, got %d", DefaultTmpSizeCapMiB, got)
	}
	if got := ResolveTmpSizeMiB("4G", DefaultMemoryMiB); got != 4096 {
		t.Errorf("expected 4096 for 4G, got %d", got)
	}
}

// TestResolveTmpSizeMiB_MemoryRelativeDefault pins the unset default:
// min(DefaultTmpSizeCapMiB, memory/2). The default at the default memory is the
// historical 2048 MiB, so an existing default project's tmp is unchanged.
func TestResolveTmpSizeMiB_MemoryRelativeDefault(t *testing.T) {
	cases := []struct{ memoryMiB, want uint32 }{
		{DefaultMemoryMiB, DefaultTmpSizeCapMiB},
		{8192, DefaultTmpSizeCapMiB},
		{2048, 1024},
		{128, 64},
	}
	for _, tc := range cases {
		if got := ResolveTmpSizeMiB("", tc.memoryMiB); got != tc.want {
			t.Errorf("ResolveTmpSizeMiB(\"\", %d) = %d, want %d", tc.memoryMiB, got, tc.want)
		}
	}
}

func TestValidateSizes(t *testing.T) {
	cases := []struct {
		name        string
		memory      string
		tmpSize     string
		diskSize    string
		quota       string
		wantErrPart string
	}{
		{name: "all defaults"},
		{name: "typical", memory: "8G", tmpSize: "2G", diskSize: "16G", quota: "16G"},
		{name: "tmp set with default memory", tmpSize: "2G"},
		{name: "memory at floor", memory: "128M"},
		{name: "tmp just below memory", memory: "4G", tmpSize: "3968M"},
		{name: "tmp below floor memory is implicit", memory: "128M", tmpSize: "64M"},
		{name: "memory below floor", memory: "64M", wantErrPart: "--memory"},
		{name: "memory zero", memory: "0", wantErrPart: "--memory"},
		{name: "memory unparseable", memory: "bogus", wantErrPart: "invalid --memory"},
		{name: "tmp zero", memory: "4G", tmpSize: "0", wantErrPart: "--tmp-size"},
		{name: "tmp at memory", memory: "4G", tmpSize: "4G", wantErrPart: "--tmp-size"},
		{name: "tmp at default memory", tmpSize: "4096M", wantErrPart: "must be below memory 4096M"},
		{name: "tmp at explicit memory names it", memory: "2G", tmpSize: "2G", wantErrPart: "must be below memory 2G"},
		{name: "tmp above memory", memory: "4G", tmpSize: "8G", wantErrPart: "--tmp-size"},
		{name: "tmp unparseable", memory: "4G", tmpSize: "bogus", wantErrPart: "invalid --tmp-size"},
		{name: "disk zero", diskSize: "0", wantErrPart: "--disk-size"},
		{name: "disk unparseable", diskSize: "bogus", wantErrPart: "invalid --disk-size"},
		{name: "quota zero", quota: "0", wantErrPart: "--workspace-quota"},
		{name: "quota unparseable", quota: "bogus", wantErrPart: "invalid --workspace-quota"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateSizes(tc.memory, tc.tmpSize, tc.diskSize, tc.quota)
			if tc.wantErrPart == "" {
				if err != nil {
					t.Fatalf("ValidateSizes(%q, %q, %q, %q) = %v, want nil",
						tc.memory, tc.tmpSize, tc.diskSize, tc.quota, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("ValidateSizes(%q, %q, %q, %q) = nil, want an error containing %q",
					tc.memory, tc.tmpSize, tc.diskSize, tc.quota, tc.wantErrPart)
			}
			if !strings.Contains(err.Error(), tc.wantErrPart) {
				t.Errorf("error = %q, want to contain %q", err, tc.wantErrPart)
			}
		})
	}
}

func TestResolveWorkspaceQuotaMiB_DelegatesToParseMemoryOK(t *testing.T) {
	if got := ResolveWorkspaceQuotaMiB(""); got != DefaultWorkspaceQuotaMiB {
		t.Errorf("expected default %d for empty, got %d", DefaultWorkspaceQuotaMiB, got)
	}
	if got := ResolveWorkspaceQuotaMiB("bogus"); got != DefaultWorkspaceQuotaMiB {
		t.Errorf("expected default %d for bogus, got %d", DefaultWorkspaceQuotaMiB, got)
	}
	if got := ResolveWorkspaceQuotaMiB("16G"); got != 16*1024 {
		t.Errorf("expected 16384 for 16G, got %d", got)
	}
	// 1G differs from the default, so a parsed value must win over the fallback.
	if got := ResolveWorkspaceQuotaMiB("1G"); got != 1024 {
		t.Errorf("expected 1024 for 1G, got %d", got)
	}
}

func TestParseMemoryGiB(t *testing.T) {
	if got := ParseMemoryGiB(4); got != 4096 {
		t.Errorf("ParseMemoryGiB(4) = %d, want 4096", got)
	}
	if got := ParseMemoryGiB(1); got != 1024 {
		t.Errorf("ParseMemoryGiB(1) = %d, want 1024", got)
	}
}

func TestDocumentedSizeDefaults(t *testing.T) {
	if DefaultMemoryMiB != 4096 {
		t.Errorf("DefaultMemoryMiB = %d, want 4096", DefaultMemoryMiB)
	}
	if DefaultTmpSizeCapMiB != 2048 {
		t.Errorf("DefaultTmpSizeCapMiB = %d, want 2048", DefaultTmpSizeCapMiB)
	}
	if MinMemoryMiB != 128 {
		t.Errorf("MinMemoryMiB = %d, want 128", MinMemoryMiB)
	}
	if DefaultWorkspaceQuotaMiB != 16384 {
		t.Errorf("DefaultWorkspaceQuotaMiB = %d, want 16384", DefaultWorkspaceQuotaMiB)
	}
	if MaxSandboxNameLen != 128 {
		t.Errorf("MaxSandboxNameLen = %d, want 128", MaxSandboxNameLen)
	}
	if mibPerGib != 1024 {
		t.Errorf("mibPerGib = %d, want 1024", mibPerGib)
	}
}

// TestResolveDiskSizeMiB pins the root-disk size: the configured value, or the
// microsandbox runtime's default managed root when unset or unparseable.
func TestResolveDiskSizeMiB(t *testing.T) {
	for spec, want := range map[string]uint32{"": DefaultRootDiskMiB, "bogus": DefaultRootDiskMiB, "20G": 20480} {
		if got := ResolveDiskSizeMiB(spec); got != want {
			t.Errorf("ResolveDiskSizeMiB(%q) = %d, want %d", spec, got, want)
		}
	}
	if DefaultRootDiskMiB != 4096 {
		t.Errorf("DefaultRootDiskMiB = %d, want the runtime's 4 GiB default", DefaultRootDiskMiB)
	}
}
