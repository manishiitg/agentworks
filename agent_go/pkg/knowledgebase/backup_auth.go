package knowledgebase

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
)

func (s *Service) backupCipher() (cipher.AEAD, error) {
	if len(s.cfg.BackupEncryptionKey) != 32 {
		return nil, kbErr("STORAGE_UNAVAILABLE", "The host backup encryption key is unavailable.")
	}
	block, err := aes.NewCipher([]byte(s.cfg.BackupEncryptionKey))
	if err != nil {
		return nil, kbErr("STORAGE_UNAVAILABLE", "Backup credential encryption is unavailable.")
	}
	return cipher.NewGCM(block)
}
func (s *Service) backupCredentialAAD(d backupDestination) []byte {
	return []byte("agentworks:knowledgebase:backup:" + s.cfg.OrganizationID + "\x00" + d.Remote + "\x00" + d.Username)
}
func (s *Service) encryptBackupPAT(d backupDestination, pat string) (string, error) {
	aead, err := s.backupCipher()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return "", kbErr("STORAGE_UNAVAILABLE", "Backup credential encryption is unavailable.")
	}
	return base64.StdEncoding.EncodeToString(aead.Seal(nonce, nonce, []byte(pat), s.backupCredentialAAD(d))), nil
}
func (s *Service) decryptBackupPAT(d backupDestination) (string, error) {
	aead, err := s.backupCipher()
	if err != nil {
		return "", err
	}
	raw, err := base64.StdEncoding.DecodeString(d.EncryptedPAT)
	if err != nil || len(raw) < aead.NonceSize() {
		return "", kbErr("STORAGE_UNAVAILABLE", "The saved backup credential is invalid.")
	}
	value, err := aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], s.backupCredentialAAD(d))
	if err != nil {
		return "", kbErr("STORAGE_UNAVAILABLE", "The saved backup credential cannot be decrypted.")
	}
	return string(value), nil
}

type backupGitCredential struct {
	remote, username, pat    string
	deployment, allowPrivate bool
}
type backupGitCredentialKey struct{}

func (s *Service) withBackupCredentials(ctx context.Context) (context.Context, error) {
	d, err := s.configuredBackupDestination()
	if err != nil {
		return ctx, err
	}
	pat := ""
	if d.PATSecret != "" {
		value, found := "", false
		if s.cfg.SecretResolver != nil {
			value, found = s.cfg.SecretResolver(d.PATSecret)
		}
		if !found || value == "" {
			return ctx, kbErr("BACKUP_SECRET_MISSING", "The backup token secret "+d.PATSecret+" is missing. Add it under Secrets, then retry.")
		}
		pat = value
	} else if d.EncryptedPAT != "" {
		pat, err = s.decryptBackupPAT(d)
		if err != nil {
			return ctx, err
		}
	}
	return context.WithValue(ctx, backupGitCredentialKey{}, backupGitCredential{remote: d.Remote, username: d.Username, pat: pat, deployment: s.cfg.BackupRemote != "", allowPrivate: s.cfg.AllowPrivateBackup}), nil
}

// Secret material is passed only in the child environment. URL-scoped headers,
// disabled redirects and an empty credential helper prevent credential leakage
// to other remotes or host credential caches. Nothing is written to .git/config.
func backupGitAuth(ctx context.Context, args []string) ([]string, []string, error) {
	env := gitEnvironment()
	if len(args) == 0 || (args[0] != "fetch" && args[0] != "push" && args[0] != "ls-remote") {
		return nil, env, nil
	}
	flags := []string{"-c", "http.followRedirects=false", "-c", "credential.helper="}
	cred, ok := ctx.Value(backupGitCredentialKey{}).(backupGitCredential)
	if !ok || cred.remote == "" {
		return nil, nil, kbErr("BACKUP_NOT_CONFIGURED", "A configured backup destination is required.")
	}
	resolve, err := backupNetworkResolve(ctx, cred, netBackupLookup)
	if err != nil {
		return nil, nil, err
	}
	if resolve != "" {
		// Clear inherited/repository pins and proxies before setting the checked
		// address. libcurl keeps TLS validation and SNI on the original hostname.
		flags = append(flags, "-c", "http.proxy=", "-c", "http.curloptResolve=", "-c", "http.curloptResolve="+resolve)
	}
	if cred.pat != "" {
		header := "Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte(cred.username+":"+cred.pat))
		env = append(env, "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http."+cred.remote+".extraHeader", "GIT_CONFIG_VALUE_0="+header)
	}
	return flags, env, nil
}
