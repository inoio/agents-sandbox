package agent

import (
	"os"
	"path/filepath"
	"slices"
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

// IsContentTreePattern reports whether pattern selects a nested content tree
// rather than a single file: a non-negated gitignore directory pattern (a
// trailing slash), such as an agent's skills/, commands/, agents/, prompts/,
// extensions/, themes/, workflows/ or recipes/ tree. A content tree holds
// user-authored files that the agent may also write inside the sandbox, so the
// launcher must not treat its files as launcher-managed config. A negation
// (a leading '!') is never a content tree: it only narrows a broader selection.
func IsContentTreePattern(pattern string) bool {
	return strings.HasSuffix(pattern, "/") && !strings.HasPrefix(pattern, "!")
}

// IsBroadGlobPattern reports whether pattern is a broad recursive glob ("**"),
// which selects every entry under its rule's dir at every depth. A broad glob
// cannot be classified by shape into the single launcher-managed files it
// selects: it also selects the user-authored trees (skills, themes, plugins)
// nested beneath the dir. A caller that needs only the launcher-managed files
// narrows a broad-glob rule to the dir's top level, where the launcher's config
// and credential files live.
func IsBroadGlobPattern(pattern string) bool {
	return pattern == "**"
}

// SelectManagedProvisionFile reports whether the path relative to rule.Dir is a
// single launcher-managed file — a config or credential file the stale-removal
// set or the first-run host probe may act on — rather than nested user content.
// It is SelectProvisionRule narrowed for a broad-glob rule: a broad recursive
// glob ("**") selects every file under the rule dir, including the agent's
// skills, themes, and plugins, but the launcher manages only the config and
// credential files directly under the dir. A rule without a broad glob keeps
// SelectProvisionRule's selection, minus directories.
func SelectManagedProvisionFile(rule ProvisionRule, rel string, isDir bool) bool {
	if !SelectProvisionRule(rule, rel, isDir) {
		return false
	}
	if !ruleHasBroadGlob(rule) {
		return !isDir
	}
	return !isDir && !strings.Contains(strings.Trim(rel, "/"), "/")
}

func ruleHasBroadGlob(rule ProvisionRule) bool {
	return slices.ContainsFunc(rule.Patterns, IsBroadGlobPattern)
}

// ProvisionRulesWithoutContentTrees returns rules with every content-tree
// pattern removed, dropping a rule left with no patterns. It is for callers
// that must consider only the single files the launcher manages (config and
// credential files) and never the user-authored trees beneath them: the
// disabled drop-in's stale-removal set and the first-run host probe. A
// broad-glob rule survives this filter; SelectManagedProvisionFile narrows it
// to the rule dir's top level when a caller walks it.
func ProvisionRulesWithoutContentTrees(rules []ProvisionRule) []ProvisionRule {
	filtered := make([]ProvisionRule, 0, len(rules))
	for _, rule := range rules {
		patterns := make([]string, 0, len(rule.Patterns))
		for _, pattern := range rule.Patterns {
			if !IsContentTreePattern(pattern) {
				patterns = append(patterns, pattern)
			}
		}
		if len(patterns) == 0 {
			continue
		}
		filtered = append(filtered, ProvisionRule{Dir: rule.Dir, Patterns: patterns})
	}
	return filtered
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
	return evalProvisionRules(rules, hostHome, vmHome, SelectProvisionRule, onCopy)
}

// EvalManagedProvisionRules is EvalProvisionRules narrowed to each rule's single
// launcher-managed files (SelectManagedProvisionFile): a broad-glob rule
// contributes only its dir's top-level files, so nested user content is never
// copied. It backs the disabled drop-in's stale-removal set.
func EvalManagedProvisionRules(
	rules []ProvisionRule,
	hostHome, vmHome string,
	onCopy func(dstPath string, data []byte, mode os.FileMode) error,
) (int, error) {
	return evalProvisionRules(rules, hostHome, vmHome, SelectManagedProvisionFile, onCopy)
}

func evalProvisionRules(
	rules []ProvisionRule,
	hostHome, vmHome string,
	selectFile func(rule ProvisionRule, rel string, isDir bool) bool,
	onCopy func(dstPath string, data []byte, mode os.FileMode) error,
) (int, error) {
	total := 0
	for _, rule := range rules {
		if rule.Dir == "" {
			continue
		}
		srcRoot := filepath.Join(hostHome, rule.Dir)
		n, err := walkRule(rule, srcRoot, vmHome, selectFile, onCopy)
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
			if p == "!" {
				warnings = append(warnings, "bare '!' in provision rule for "+rule.Dir)
			}
		}
	}
	return warnings
}

// walkRule copies every selected file under srcRoot into vmHome, pruning
// unselected directories (e.g., node_modules).
func walkRule(
	rule ProvisionRule,
	srcRoot, vmHome string,
	selectFile func(rule ProvisionRule, rel string, isDir bool) bool,
	onCopy func(string, []byte, os.FileMode) error,
) (int, error) {
	var count int
	err := filewalk.Walk(
		srcRoot,
		func(rel string, isDir bool) bool { return selectFile(rule, rel, isDir) },
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
