package naming

import "strings"

// ParseProjectTarget parses a purge target: a project slug, or a slug joined to
// an agent by '-'. The slug embeds the 14-character base36 project hash, so the
// split is unambiguous — the agent is whatever follows the hash — and a target
// without the hash is foreign and rejected. The results are the slug, the agent
// (empty for a bare slug), and whether the target is one of ours. Examples:
//
//	"myproj-1mjusbm3wikhb0"             → slug="myproj-1mjusbm3wikhb0", agent="", ok=true
//	"myproj-1mjusbm3wikhb0-opencode"    → slug="myproj-1mjusbm3wikhb0", agent="opencode", ok=true
//	"myproj-1mjusbm3wikhb0-claude-code" → slug="myproj-1mjusbm3wikhb0", agent="claude-code", ok=true
//	"myproj-opencode"                   → ok=false (no slug hash)
func ParseProjectTarget(target string) (string, string, bool) {
	hashStart := findHashSuffix(target)
	if hashStart == -1 {
		return "", "", false
	}
	slug := target[:hashStart+14]
	rest := target[hashStart+14:]
	if rest == "" {
		return slug, "", true
	}
	// findHashSuffix bounds the hash by '-' or the end, so a non-empty rest
	// always begins with '-'; stripping it leaves the whole agent, which may
	// itself be hyphenated.
	agent := strings.TrimPrefix(rest, "-")
	if agent == "" {
		return "", "", false
	}
	return slug, agent, true
}
