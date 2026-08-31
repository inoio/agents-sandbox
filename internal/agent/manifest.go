package agent

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
)

// ProvisionRule scopes a gitignore-style pattern list to a home-relative dir.
// Patterns are relative to Dir; files selected for copy are placed at the same
// relative path in the VM.
type ProvisionRule struct {
	Dir      string
	Patterns []string
}

func (r ProvisionRule) matcher() gitignore.Matcher {
	ps := make([]gitignore.Pattern, 0, len(r.Patterns))
	for _, p := range r.Patterns {
		ps = append(ps, gitignore.ParsePattern(p, nil))
	}
	return gitignore.NewMatcher(ps)
}

// SelectProvisionRule reports whether the path relative to rule.Dir is selected
// for copy. A selected path is one the gitignore matcher marks as excluded
// (Match returns true), which we interpret as "include". Directories that are
// not selected should be pruned (not descended into).
func SelectProvisionRule(rule ProvisionRule, rel string, isDir bool) bool {
	segments := splitSegments(rel)
	return rule.matcher().Match(segments, isDir)
}

func splitSegments(rel string) []string {
	rel = strings.Trim(rel, "/")
	if rel == "" {
		return nil
	}
	return strings.Split(rel, "/")
}

// EvalProvisionRules walks each rule's host dir and copies every selected file
// to the same relative path under vmHome, pruning unselected directories. It
// returns the count of files copied. onCopy is invoked (when non-nil) with the
// destination VM path and content for each copied file; when nil the file is
// copied directly from host to vmHome.
func EvalProvisionRules(
	rules []ProvisionRule,
	hostHome, vmHome string,
	onCopy func(dstPath string, data []byte) error,
) (int, error) {
	total := 0
	for _, rule := range rules {
		if rule.Dir == "" {
			continue
		}
		srcRoot := filepath.Join(hostHome, rule.Dir)
		n, err := walkRule(rule, srcRoot, vmHome, onCopy)
		if err != nil {
			return total, err
		}
		total += n
	}
	return total, nil
}

// ValidateProvisionRules returns user-facing warnings for patterns that can
// never affect the result or that are malformed.
func ValidateProvisionRules(rules []ProvisionRule) []string {
	var warnings []string
	for _, rule := range rules {
		for _, p := range rule.Patterns {
			if p == "" {
				warnings = append(warnings, "empty pattern in provision rule for "+rule.Dir)
			}
			if strings.HasPrefix(p, "!") && p == "!" {
				warnings = append(warnings, "bare '!' in provision rule for "+rule.Dir)
			}
		}
	}
	return warnings
}

func walkRule(rule ProvisionRule, srcRoot, vmHome string, onCopy func(string, []byte) error) (int, error) {
	if _, err := os.Stat(srcRoot); err != nil {
		return 0, nil //nolint:nilerr // host dir absent: nothing to copy
	}
	var count int
	err := filepath.WalkDir(srcRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // skip unreadable entries; never fail the session
		}
		rel, relErr := filepath.Rel(srcRoot, path)
		if relErr != nil || rel == "." {
			return nil //nolint:nilerr // skip entries outside the walk root
		}
		if !SelectProvisionRule(rule, rel, d.IsDir()) {
			if d.IsDir() {
				return filepath.SkipDir // prune excluded dir (e.g. node_modules)
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		data, readErr := os.ReadFile(path) //nolint:gosec // reads within the host dir we own
		if readErr != nil {
			return nil //nolint:nilerr // skip unreadable file; never fail the session
		}
		dst := filepath.Join(vmHome, rule.Dir, rel)
		if onCopy != nil {
			if err := onCopy(dst, data); err != nil {
				return err
			}
		}
		count++
		return nil
	})
	return count, err
}
