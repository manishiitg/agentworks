package knowledgebase

import (
	"context"
	"net"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

// Match Vault's public-address policy, including ranges not covered by IsPrivate.
var backupBlockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("2001:db8::/32"),
}

func backupPublicAddress(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsValid() || !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() || addr.Zone() != "" {
		return false
	}
	for _, prefix := range backupBlockedPrefixes {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}
func validateBackupLiteralHost(remote string, allowPrivate bool) error {
	u, err := url.Parse(remote)
	if err != nil {
		return badArg("Invalid backup URL.")
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if allowPrivate {
		return nil
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return badArg("Backup hosts must use public network addresses.")
	}
	if addr, err := netip.ParseAddr(host); err == nil && !backupPublicAddress(addr) {
		return badArg("Backup hosts must use public network addresses.")
	}
	return nil
}
func netBackupLookup(ctx context.Context, host string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}

// Resolve immediately before each Git transport and pin that checked address in
// libcurl. A DNS precheck alone would leave a second lookup/rebinding window.
func backupNetworkResolve(ctx context.Context, cred backupGitCredential, lookup func(context.Context, string) ([]netip.Addr, error)) (string, error) {
	if !strings.HasPrefix(cred.remote, "https://") {
		// SSH/local destinations are selected only by the operator.
		if cred.deployment {
			return "", nil
		}
		return "", badArg("SSH backups require deployment configuration.")
	}
	if !validBackupRemote(cred.remote) {
		return "", badArg("Invalid backup URL.")
	}
	if err := validateBackupLiteralHost(cred.remote, cred.allowPrivate); err != nil {
		return "", err
	}
	u, _ := url.Parse(cred.remote)
	host := u.Hostname()
	var addresses []netip.Addr
	if addr, err := netip.ParseAddr(host); err == nil {
		addresses = []netip.Addr{addr}
	} else {
		lookupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		var err error
		addresses, err = lookup(lookupCtx, host)
		if err != nil || len(addresses) == 0 {
			return "", kbErr("BACKUP_UNAVAILABLE", "Backup host DNS lookup failed.")
		}
	}
	for _, addr := range addresses {
		if !addr.IsValid() || addr.Zone() != "" || !cred.allowPrivate && !backupPublicAddress(addr) {
			return "", badArg("Backup host resolves to a private or special-use address.")
		}
	}
	port := u.Port()
	if port == "" {
		port = "443"
	}
	ip := addresses[0].Unmap().String()
	if addresses[0].Unmap().Is6() {
		ip = "[" + ip + "]"
	}
	return host + ":" + port + ":" + ip, nil
}
