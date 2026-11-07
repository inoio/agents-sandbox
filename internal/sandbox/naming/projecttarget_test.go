package naming

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestParseProjectTargetSlugAndAgent covers the two target forms a purge
// accepts: a bare hash-anchored slug (whole project) and a slug joined to an
// agent by '-'. The slug embeds the 14-character project hash, so a hyphenated
// agent (claude-code) or a folder whose own last segment is hash-shaped cannot
// confuse the split.
func TestParseProjectTargetSlugAndAgent(t *testing.T) {
	cases := []struct {
		target    string
		wantSlug  string
		wantAgent string
	}{
		{"myproj-1mjusbm3wikhb0", "myproj-1mjusbm3wikhb0", ""},
		{"myproj-1mjusbm3wikhb0-opencode", "myproj-1mjusbm3wikhb0", "opencode"},
		{"myproj-1mjusbm3wikhb0-claude-code", "myproj-1mjusbm3wikhb0", "claude-code"},
		{"go-1234567890abcd-1mjusbm3wikhb0", "go-1234567890abcd-1mjusbm3wikhb0", ""},
		{"go-1234567890abcd-1mjusbm3wikhb0-opencode", "go-1234567890abcd-1mjusbm3wikhb0", "opencode"},
	}
	for _, tc := range cases {
		slug, agent, ok := ParseProjectTarget(tc.target)
		require.True(t, ok, tc.target)
		require.Equal(t, tc.wantSlug, slug, tc.target)
		require.Equal(t, tc.wantAgent, agent, tc.target)
	}
}

// TestParseProjectTargetRejectsForeign pins that only a hash-anchored slug
// (with or without an agent) is a valid target: a bare project name, a name
// without the 14-character hash, a bare hash without a folder, an empty agent
// and an uppercase (non-base36) hash all fail closed.
func TestParseProjectTargetRejectsForeign(t *testing.T) {
	foreign := []string{
		"",
		"myproj",
		"myproj-opencode",
		"1mjusbm3wikhb0",
		"myproj-1mjusbm3wikhb0-",
		"myproj-1MJUSBM3WIKHB0-opencode",
	}
	for _, target := range foreign {
		_, _, ok := ParseProjectTarget(target)
		require.False(t, ok, target)
	}
}
