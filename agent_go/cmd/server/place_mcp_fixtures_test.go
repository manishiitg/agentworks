package server

import (
	"fmt"
	"path/filepath"
	"time"
)

// Test fixtures for the one-off Vault migrations: they write the retired place store the migrations read.
// The place store is retired (PLAT-856-217); nothing in the server writes it any more.

func setPersonalSecret(userID, name, value string) error {
	if !personalSecretNamePattern.MatchString(name) {
		return fmt.Errorf("secret names are UPPER_SNAKE_CASE, up to 64 characters")
	}
	if value == "" {
		return fmt.Errorf("a secret needs a value")
	}
	sealed, err := encryptSecretValueWithAAD(value, personalSecretAAD(userID, name))
	if err != nil {
		return err
	}
	placeMCPMu.Lock()
	defer placeMCPMu.Unlock()
	dir, err := placeMCPDir(userID)
	if err != nil {
		return err
	}
	secrets := map[string]string{}
	if err := readPlaceMCPJSON(filepath.Join(dir, "secrets.json"), &secrets); err != nil {
		return err
	}
	secrets[name] = sealed
	return writePlaceMCPJSON(filepath.Join(dir, "secrets.json"), secrets)
}

func attachPersonalLoginToPlace(owner, server, root string) error {
	placeMCPMu.Lock()
	defer placeMCPMu.Unlock()
	all, err := readPlaceMCPAttachmentsLocked()
	if err != nil {
		return err
	}
	for _, a := range all[root] {
		if a.Owner == owner && a.Server == server {
			return nil
		}
	}
	all[root] = append(all[root], placeMCPAttachment{Owner: owner, Server: server, Scope: "user", AttachedAt: time.Now().UTC().Format(time.RFC3339)})
	return writePlaceMCPAttachmentsLocked(all)
}
