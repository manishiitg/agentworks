package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

var gmailSetupConfigMu sync.Mutex

func gmailSetupConfigPath() (string, error) {
	root, err := workflowCLIStateRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "gmail-inbound", "config.json"), nil
}

func loadGmailSetupConfig(c gmailInboundConfig) (gmailInboundConfig, error) {
	path, err := gmailSetupConfigPath()
	if err != nil {
		return c, err
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return c, nil
	}
	if err != nil {
		return c, fmt.Errorf("cannot read private Gmail setup configuration")
	}
	var saved gmailInboundConfig
	if json.Unmarshal(raw, &saved) != nil {
		return c, fmt.Errorf("invalid private Gmail setup configuration")
	}
	return saved, nil
}

func writeGmailSetupConfig(c gmailInboundConfig) error {
	path, err := gmailSetupConfigPath()
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("cannot create private Gmail setup directory")
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".config-*")
	if err != nil {
		return fmt.Errorf("cannot save private Gmail setup configuration")
	}
	defer os.Remove(f.Name())
	raw, _ := json.MarshalIndent(c, "", "  ")
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(raw)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	if err != nil {
		return fmt.Errorf("cannot save private Gmail setup configuration")
	}
	return nil
}
