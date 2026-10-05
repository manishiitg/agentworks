package browserrelay

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// Private server state is needed to return the same code to its authenticated
// owner. Codes are never included in status, logs or selection state.
func (m *Manager) loadCredentials() error {
	data, err := os.ReadFile(m.credentialsPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var credentials map[string]grant
	if err := json.Unmarshal(data, &credentials); err != nil {
		return err
	}
	if len(credentials) > 1000 {
		return errors.New("too many saved browser credentials")
	}
	scopes := map[string]bool{}
	for token, g := range credentials {
		decoded, err := base64.RawURLEncoding.DecodeString(token)
		scope := key(g.User, g.Scope)
		if err != nil || len(decoded) != 32 || g.User == "" || g.Scope == "" || scopes[scope] || len(scope) > 8192 || len(g.Label) > 8192 || len(g.ProfileID) > 8192 {
			return errors.New("invalid saved browser credential")
		}
		scopes[scope] = true
	}
	if credentials != nil {
		m.pairs = credentials
	}
	return nil
}
func (m *Manager) persistCredentials(credentials map[string]grant) error {
	if m.credentialsPath == "" {
		return nil
	}
	data, err := json.Marshal(credentials)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(m.credentialsPath), ".credentials-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), m.credentialsPath)
}
