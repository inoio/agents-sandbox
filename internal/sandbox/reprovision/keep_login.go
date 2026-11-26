package reprovision

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"
)

// homeFilesToWrite returns the home files to write this start: every one,
// except a keep-login target whose current content in the sandbox holds a
// login the agent renews. Overwriting it would sign the agent out on every
// start; a target that is absent, unreadable, or holds no renewing login is
// written.
func homeFilesToWrite(
	read func(string) ([]byte, bool),
	files map[string][]byte,
	keepLogin map[string]bool,
) map[string][]byte {
	toWrite := maps.Clone(files)
	for path := range keepLogin {
		// An absent or unreadable target reads as no content, which holds no login.
		current, _ := read(path)
		if holdsRenewingLogin(current) {
			delete(toWrite, path)
		}
	}
	return toWrite
}

// holdsRenewingLogin reports whether a JSON document holds a non-empty refresh
// token at any depth (refresh_token, refreshToken): a login the agent renews,
// such as codex's ChatGPT tokens or claude-code's OAuth credentials. An API-key
// store holds none. A document that is not JSON holds no login this can
// recognize.
func holdsRenewingLogin(data []byte) bool {
	var document any
	// A failed decode leaves document nil, which holds no refresh token.
	_ = json.Unmarshal(data, &document)
	return containsRefreshToken(document)
}

// containsRefreshToken walks a decoded JSON value for a non-empty refresh token.
func containsRefreshToken(node any) bool {
	switch typed := node.(type) {
	case map[string]any:
		for key, child := range typed {
			// A refresh token that is not a string reads as empty: no login.
			if token, _ := child.(string); token != "" && isRefreshTokenKey(key) {
				return true
			}
			if containsRefreshToken(child) {
				return true
			}
		}
	case []any:
		return slices.ContainsFunc(typed, containsRefreshToken)
	}
	return false
}

// isRefreshTokenKey reports whether key names a refresh token, in snake or
// camel case.
func isRefreshTokenKey(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(key, "_", ""))
	return normalized == "refreshtoken"
}
