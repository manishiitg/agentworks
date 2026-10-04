package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/gmailsetup"
)

var gmailSetupConfigMu sync.Mutex

func gmailSetupConfigPath() (string, error) {
	root, err := workflowCLIStateRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "gmail-inbound", "config.json"), nil
}

// Explicit environment settings and setup's private file can coexist, but
// neither can silently replace the other's receiving identity or client topic.
func mergeGmailInboundConfig(a, b gmailInboundConfig) (gmailInboundConfig, error) {
	if len(b.Topics) == 0 {
		return a, nil
	}
	if a.Topics == nil {
		a.Topics = map[string]string{}
	}
	if len(a.Topics) > 0 && (a.Audience != b.Audience || a.PushEmail != b.PushEmail) {
		return a, fmt.Errorf("Gmail setup conflicts with the deployment's environment; reconcile its audience and push identity before retrying")
	}
	for client, topic := range b.Topics {
		if old := a.Topics[client]; old != "" && old != topic {
			return a, fmt.Errorf("Gmail setup conflicts with an existing OAuth client's topic")
		}
		a.Topics[client] = topic
	}
	a.Audience = b.Audience
	a.PushEmail = b.PushEmail
	return a, nil
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
	if json.Unmarshal(raw, &saved) != nil || len(saved.Topics) == 0 {
		return c, fmt.Errorf("invalid private Gmail setup configuration")
	}
	if err = validateGmailInboundConfig(saved); err != nil {
		return c, err
	}
	return mergeGmailInboundConfig(c, saved)
}

func saveGmailSetupConfig(p gmailsetup.Plan) error {
	gmailSetupConfigMu.Lock()
	defer gmailSetupConfigMu.Unlock()
	c, err := readGmailInboundConfig()
	if err != nil {
		return err
	}
	c, err = mergeGmailInboundConfig(c, gmailInboundConfig{Topics: map[string]string{p.ClientName: p.Topic}, Audience: p.Endpoint, PushEmail: p.PushEmail})
	if err != nil {
		return err
	}
	if err = validateGmailInboundConfig(c); err != nil {
		return err
	}
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
		return fmt.Errorf("cannot save private Gmail setup configuration; cloud resources remain available for a retry")
	}
	return nil
}
