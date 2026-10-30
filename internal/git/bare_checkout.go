package git

// BareCheckoutRoots reports whether cwd lies in a bare checkout whose worktrees
// live inside the bare root, and if so returns the bare root and the root of the
// worktree cwd is in (the bare root itself when cwd is the bare root). A linked
// worktree outside its bare checkout, a non-bare repository, a directory outside
// any repository and an unresolvable cwd all report false.
//
// An unresolvable cwd reports false rather than an error: callers use this to
// choose where project config is read from, which is not identity-keyed and must
// not fail the read. Slug-deriving callers use ResolveIdentity, which refuses
// instead (#354).
func BareCheckoutRoots(cwd string) (string, string, bool) {
	identity, err := ResolveIdentity(cwd)
	if err != nil || !identity.Bare || identity.OutsideWorktree {
		return "", "", false
	}
	return identity.CheckoutRoot, identity.WorktreeRoot, true
}
