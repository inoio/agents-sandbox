package options

import (
	"math"
	"math/big"
	"strconv"
	"strings"
	"testing"
)

// FuzzParseOptions drives the two parsers fed user-authored specs: ParseMemoryOK
// for a `--memory`/`tmp_size`/`workspace_quota` size and ResolveWorktreeSpec for
// `--worktree`. Neither may panic on a hostile spec. A size that parses must
// equal the true count scaled by its suffix with no uint32 wrap (an overflow
// silently produces a wrong-sized VM); a parsed worktree name must already be a
// slug, because the launcher matches it against a daemon-slugified directory.
func FuzzParseOptions(f *testing.F) {
	for _, seed := range []string{
		"", "0", "512M", "4G", "4096", "2048", "  3g  ", "4294967295",
		"4194303G", "4194304G", "5000000000", "4294967296", "1e3", "-1",
		"4GB", "g", "M", "+4G", "0G",
		"my-worktree", "my-worktree:main", "My_Worktree", "a:b:c", "  spaced  ",
		"with space", "-leading", "trailing-", "a--b", "UPPER",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, spec string) {
		got, ok := ParseMemoryOK(spec)
		want, wantOK := modelParseMemoryOK(spec)
		if ok != wantOK {
			t.Fatalf("ParseMemoryOK(%q) ok = %v, want %v", spec, ok, wantOK)
		}
		if ok && got != want {
			t.Fatalf("ParseMemoryOK(%q) = %d, want %d", spec, got, want)
		}

		worktree, err := ResolveWorktreeSpec(spec)
		if err != nil {
			return
		}
		if strings.TrimSpace(spec) == "" {
			if worktree != (WorktreeSpec{}) {
				t.Fatalf("ResolveWorktreeSpec(%q) = %+v, want zero for blank input", spec, worktree)
			}
			return
		}
		if strings.Contains(worktree.Name, ":") {
			t.Fatalf("ResolveWorktreeSpec(%q) name %q contains a separator", spec, worktree.Name)
		}
		if slugify(worktree.Name) != worktree.Name {
			t.Fatalf("ResolveWorktreeSpec(%q) accepted non-slug name %q", spec, worktree.Name)
		}
	})
}

// modelParseMemoryOK is an independent oracle for ParseMemoryOK. It computes the
// scaled count with math/big, so a uint32 overflow in the parser shows up as a
// mismatch instead of the oracle agreeing with the same bug.
func modelParseMemoryOK(spec string) (uint32, bool) {
	trimmed := strings.TrimSpace(spec)
	if trimmed == "" {
		return 0, false
	}
	multiplier := uint64(1)
	rest := trimmed
	switch trimmed[len(trimmed)-1] {
	case 'g', 'G':
		multiplier = 1024
		rest = trimmed[:len(trimmed)-1]
	case 'm', 'M':
		multiplier = 1
		rest = trimmed[:len(trimmed)-1]
	}
	count, err := strconv.ParseUint(rest, 10, 64)
	if err != nil {
		return 0, false
	}
	product := new(big.Int).Mul(new(big.Int).SetUint64(count), new(big.Int).SetUint64(multiplier))
	if !product.IsUint64() || product.Uint64() > math.MaxUint32 {
		return 0, false
	}
	return uint32(product.Uint64()), true
}
