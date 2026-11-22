package firstrun

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/inoio/agents-sandbox/internal/agent"
)

// skillTreeNames are the allowlist trees whose top-level directories are
// skills: a skill is copied whole or not at all.
var skillTreeNames = []string{"skills", "skill"}

// instructionSource is one host file the seed may copy: its path as the user
// sees it (under the allowlist entry, through any symlink), the resolved path
// it is read from, and its path relative to the mirror root.
type instructionSource struct {
	hostPath string
	readPath string
	rel      string
}

// instructionUnit is what the seed admits or refuses as a whole: one
// instruction file, or one skill directory with every file in it.
type instructionUnit struct {
	name    string
	sources []instructionSource
}

// instructionPlan is the seed's view of a host's instruction files: the units
// it may copy, sorted by name, and what it rejected while planning.
type instructionPlan struct {
	units          []instructionUnit
	scriptedSkills []string
	escapingLinks  []string
}

// planInstructions walks a's instruction allowlist under hostHome. Only
// Markdown files are candidates; .git and node_modules are never descended
// into. A symlink is followed when its target resolves inside the host home
// (a dotfiles checkout) and is otherwise rejected and recorded, as is a link to
// the home itself; every file keeps its path under the allowlist entry and is
// read from its resolved path. Under a skills tree each top-level directory is
// one unit, rejected whole when it holds a non-Markdown file or a rejected link;
// every other Markdown file is its own unit. The units are in allowlist order,
// each tree in lexical order, which is the order the seed's caps admit them in.
func planInstructions(a agent.Agent, hostHome string) instructionPlan {
	root, entries := instructionAllowlist(a, hostHome)
	// An unresolvable home leaves home empty, so every path is refused as
	// outside it.
	home, _ := filepath.EvalSymlinks(hostHome)
	walker := instructionWalker{home: home, visited: map[string]bool{}, escapingLinks: nil}
	var plan instructionPlan
	for _, entry := range entries {
		rel := strings.TrimSuffix(entry, "/")
		sources := walker.collect(filepath.Join(root, rel), rel)
		if slices.Contains(skillTreeNames, rel) {
			plan.addSkillTree(rel, sources)
		} else {
			plan.addFiles(sources)
		}
	}
	plan.escapingLinks = walker.escapingLinks
	return plan
}

// addFiles adds every Markdown file of sources as its own unit.
func (p *instructionPlan) addFiles(sources []instructionSource) {
	for _, source := range sources {
		if source.readPath != "" && isInstructionFileName(source.rel) {
			p.units = append(p.units, instructionUnit{name: source.rel, sources: []instructionSource{source}})
		}
	}
}

// addSkillTree adds each skill directory under treeRel as one unit, and a
// Markdown file directly in the tree as its own, in the tree's lexical order. A
// skill holding a non-Markdown file is recorded as scripted; one holding a
// rejected link is incomplete. Either is left out whole.
func (p *instructionPlan) addSkillTree(treeRel string, sources []instructionSource) {
	var unitNames []string
	units := map[string][]instructionSource{}
	for _, source := range sources {
		inTree, _ := filepath.Rel(treeRel, source.rel)
		skill, _, _ := strings.Cut(inTree, string(filepath.Separator))
		name := filepath.Join(treeRel, skill)
		if _, seen := units[name]; !seen {
			unitNames = append(unitNames, name)
		}
		units[name] = append(units[name], source)
	}
	for _, name := range unitNames {
		unitSources := units[name]
		switch {
		case unitSources[0].rel == name:
			p.addFiles(unitSources)
		case slices.ContainsFunc(unitSources, isRejectedLink):
		case slices.ContainsFunc(unitSources, isNotMarkdown):
			p.scriptedSkills = append(p.scriptedSkills, name)
		default:
			p.units = append(p.units, instructionUnit{name: name, sources: unitSources})
		}
	}
}

func isRejectedLink(source instructionSource) bool { return source.readPath == "" }

func isNotMarkdown(source instructionSource) bool { return !isInstructionFileName(source.rel) }

// instructionWalker collects the files under allowlist entries, following
// symlinks that stay inside home and recording those that do not.
type instructionWalker struct {
	home          string
	visited       map[string]bool
	escapingLinks []string
}

// collect returns the regular files at hostPath (the file itself, or every file
// in the tree below it) as sources relative to the mirror root. A rejected link
// is returned with an empty readPath so a skill holding it is not taken whole.
func (w *instructionWalker) collect(hostPath, rel string) []instructionSource {
	readPath, info, result := w.resolve(hostPath)
	if result == linkRejected {
		return []instructionSource{{hostPath: hostPath, readPath: "", rel: rel}}
	}
	if result == pathMissing {
		return nil
	}
	if info.Mode().IsRegular() {
		return []instructionSource{{hostPath: hostPath, readPath: readPath, rel: rel}}
	}
	// A visited directory is a link cycle.
	if w.visited[readPath] {
		return nil
	}
	w.visited[readPath] = true
	// A non-directory or an unreadable one lists no entries.
	entries, _ := os.ReadDir(readPath)
	var sources []instructionSource
	for _, entry := range entries {
		if slices.Contains(prunedInstructionDirs, entry.Name()) {
			continue
		}
		sources = append(sources, w.collect(
			filepath.Join(hostPath, entry.Name()),
			filepath.Join(rel, entry.Name()),
		)...)
	}
	return sources
}

// resolution is how resolve settled a path.
type resolution int

const (
	pathResolved resolution = iota
	pathMissing
	linkRejected
)

// resolve returns hostPath's fully resolved path and its target's info. Every
// path must resolve inside the home, and not to the home itself, so a symlink
// leaving the home, pointing at it, or dangling is recorded as escaping and
// rejected, while a link into a dotfiles checkout in the home is followed.
func (w *instructionWalker) resolve(hostPath string) (string, os.FileInfo, resolution) {
	if _, err := os.Lstat(hostPath); err != nil {
		return "", nil, pathMissing
	}
	// A dangling link resolves to nothing, which is not inside the home.
	resolved, _ := filepath.EvalSymlinks(hostPath)
	if !strictlyInside(w.home, resolved) {
		w.escapingLinks = append(w.escapingLinks, hostPath)
		return "", nil, linkRejected
	}
	info, err := os.Stat(resolved)
	// gomutants:disable-next-line BRANCH_IF reason="EvalSymlinks just resolved the path, so Stat fails only when it is removed concurrently"
	if err != nil {
		// gomutants:disable-next-line RETURN_ZERO reason="unreachable without a concurrent removal, see the branch above"
		return "", nil, pathMissing
	}
	return resolved, info, pathResolved
}

// strictlyInside reports whether path lies below dir (not dir itself). A failed
// Rel leaves rel empty, which is not local.
func strictlyInside(dir, path string) bool {
	rel, _ := filepath.Rel(dir, path)
	return rel != "." && filepath.IsLocal(rel)
}
