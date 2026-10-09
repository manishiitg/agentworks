package server

import "testing"

// AUTH_ALLOWED_EMAIL_DOMAINS (PLAT-710) is a security rule: an outside address can neither
// sign in with SSO nor be added, not even when ADMIN_USERS names it, and a look-alike domain does not match.
func TestAllowedEmailDomainsGateSSOAndAccounts(t *testing.T) {
	t.Setenv("AUTH_ALLOWED_EMAIL_DOMAINS", "corp.example")
	t.Setenv("ADMIN_USERS", "owner@example.com,admin@corp.example")
	if externalAuthIdentityApproved("owner@example.com") {
		t.Fatal("an admin outside the allowed domain was approved")
	}
	if !externalAuthIdentityApproved("Admin@Corp.Example") {
		t.Fatal("an admin at the allowed domain was refused")
	}
	for _, email := range []string{"a@corp.example.evil.com", "a@evilcorp.example", "a@example.com"} {
		if emailDomainAllowed(email) {
			t.Fatalf("%s allowed", email)
		}
	}
	if msg := adminEmailError(&userDirectory{}, "someone@example.com", ""); msg == "" {
		t.Fatal("an admin could add an account outside the allowed domain")
	}
}
