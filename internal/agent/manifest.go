package agent

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/format/gitignore"

	"github.com/inoio/agents-sandbox/internal/filewalk"
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
// destination VM path, content, and ordinary permission bits for each copied
// file; when nil the file is copied directly from host to vmHome.
func EvalProvisionRules(
	rules []ProvisionRule,
	hostHome, vmHome string,
	onCopy func(dstPath string, data []byte, mode os.FileMode) error,
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

// walkRule copies every selected file under srcRoot into vmHome, pruning
// unselected directories (e.g., node_modules).
func walkRule(rule ProvisionRule, srcRoot, vmHome string, onCopy func(string, []byte, os.FileMode) error) (int, error) {
	var count int
	err := filewalk.Walk(
		srcRoot,
		func(rel string, isDir bool) bool { return SelectProvisionRule(rule, rel, isDir) },
		func(entry filewalk.Entry) error {
			dst := filepath.Join(vmHome, rule.Dir, entry.Rel)
			if onCopy != nil {
				if err := onCopy(dst, entry.Data, entry.Mode); err != nil {
					return err
				}
			}
			count++
			return nil
		},
	)
	return count, err
}
