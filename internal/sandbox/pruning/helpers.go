package pruning

import "strings"

func hasPrefix(s, prefix string) bool { return strings.HasPrefix(s, prefix) }

// imageDigest returns the digest tag (after the last ':') of an image reference,
// or "" when absent.
func imageDigest(ref string) string {
	if i := strings.LastIndex(ref, ":"); i >= 0 {
		return ref[i+1:]
	}
	return ""
}

// activeSlugs returns the set of slugs that have a running VM.
func activeSlugs(snap LiveState) map[string]bool {
	m := make(map[string]bool, len(snap.ActiveVMs))
	for slug := range snap.ActiveVMs {
		m[slug] = true
	}
	return m
}
