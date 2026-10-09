package server

// canonicalizeDirectoryUserClaims replaces a provider/external identity ID in
// an older JWT with the stable account ID from config/users.json. OAuth users
// can first receive the provider's subject ID and later be linked by email to
// a pre-provisioned password account. Every workspace path must use the
// directory ID after that link; otherwise one person acquires two chat trees.
func canonicalizeDirectoryUserClaims(claims *UserClaims) {
	if claims == nil || !IsMultiUserMode() {
		return
	}
	if rec := directoryUserForClaims(claims); rec != nil && rec.ID != "" {
		claims.UserID = rec.ID
	}
}

// directoryUserIsUnknown reports whether a still-valid token names an identity
// the user directory has no record of, once a directory exists at all.
//
// Why this matters: a JWT stays cryptographically valid for its whole
// lifetime, so a session minted before a deployment adopted the user
// directory (or before a migration renamed its users) keeps authenticating
// as a ghost. Seen live on server A 2026-09-03: the migration had moved
// `_users/default` to the admin's real ID, but a browser still held a
// pre-migration token with user_id "default" -- every request resolved to
// that phantom user, so Video Studio showed zero projects and the workflow
// list hid everything (a ghost is neither an owner nor a reader), with no
// error anywhere. Refusing the token forces one clean sign-in instead.
//
// Deliberately narrow: single-user deployments (no MULTI_USER_MODE) have no
// directory to consult; an empty directory means nothing has been adopted
// yet; and identities that never pass through AuthMiddleware (scheduler,
// bot connector, internal sessions) are unaffected. OAuth sign-ins create a
// directory record on first login, so they are known by the time they hold
// a token.
func directoryUserIsUnknown(claims *UserClaims) bool {
	if claims == nil || !IsMultiUserMode() {
		return false
	}
	dir, err := loadUserDirectory()
	if err != nil || dir == nil || len(dir.Users) == 0 {
		return false
	}
	return dir.find(claims.UserID, claims.Username, claims.Email) == nil
}
