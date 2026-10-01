package server

import (
	"log"
	"os"
	"sort"
	"strings"
)

// Child-environment audit logging. The provider env builders start from the
// full server environment and subtract a denylist; these logs show which
// variable names a spawned child actually receives, so the fail-open surface
// stays visible until the builders move to an allowlist.
//
// Names only, never values: a name list cannot leak a credential, while a
// value in a log file would. Keep it that way.
//
// Enabled by default; set LOG_CHILD_ENV=0 (or false/off/no) to disable.
func childEnvLogEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("LOG_CHILD_ENV"))) {
	case "0", "false", "off", "no":
		return false
	default:
		return true
	}
}

// envNameSet returns the sorted unique variable names in env.
func envNameSet(env []string) []string {
	seen := map[string]bool{}
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		seen[name] = true
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// diffEnvNames compares the variable names before and after an env builder
// runs: stripped names were removed, added names were injected. Both lists
// are sorted; values are never examined.
func diffEnvNames(before, after []string) (stripped, added []string) {
	inBefore := map[string]bool{}
	for _, entry := range before {
		name, _, _ := strings.Cut(entry, "=")
		inBefore[name] = true
	}
	inAfter := map[string]bool{}
	for _, entry := range after {
		name, _, _ := strings.Cut(entry, "=")
		inAfter[name] = true
	}
	for name := range inBefore {
		if !inAfter[name] {
			stripped = append(stripped, name)
		}
	}
	for name := range inAfter {
		if !inBefore[name] {
			added = append(added, name)
		}
	}
	sort.Strings(stripped)
	sort.Strings(added)
	return stripped, added
}

// logChildEnv records what a spawned child inherits: how many server variable
// names it keeps, which denylisted names were stripped, which names were
// injected, and the full sorted name list. Values are never logged.
func logChildEnv(label string, before, after []string) {
	if !childEnvLogEnabled() {
		return
	}
	stripped, added := diffEnvNames(before, after)
	kept := len(envNameSet(after)) - len(added)
	log.Printf("[CHILD_ENV] %s kept=%d stripped=[%s] injected=[%s]", label, kept, strings.Join(stripped, ","), strings.Join(added, ","))
	log.Printf("[CHILD_ENV] %s vars=[%s]", label, strings.Join(envNameSet(after), ","))
}

// providerConnectionEnvLabel identifies a provider-connection child env in
// the audit log. IDs only, never credentials.
func providerConnectionEnvLabel(provider, id string) string {
	return "provider-connection provider=" + provider + " account=" + id
}
