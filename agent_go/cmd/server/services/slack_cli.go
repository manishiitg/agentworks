package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// RunSlackCLI uses the saved connector token only in the CLI child's environment.
func RunSlackCLI(ctx context.Context, method string, parameters map[string]interface{}) (string, error) {
	return RunSlackCLIOnConnection(ctx, "", method, parameters)
}

// RunSlackCLIOnConnection runs a Slack CLI read through one named app
// connection. An empty connection ID means the default connection. An
// unknown or disabled connection fails rather than using another identity.
func RunSlackCLIOnConnection(ctx context.Context, connID, method string, parameters map[string]interface{}) (string, error) {
	slackFSMu.Lock()
	cfg, err := loadSlackConfigFromDisk()
	slackFSMu.Unlock()
	if err != nil {
		return "", fmt.Errorf("could not load Slack connector credentials")
	}
	conn, ok := effectiveSlackConnection(cfg, connID)
	if !ok {
		if strings.TrimSpace(connID) != "" {
			return "", fmt.Errorf("Slack connection is unknown or has no tokens")
		}
		return "", fmt.Errorf("Slack bot is disabled or its token is missing; configure Setup > Connectors > Slack")
	}
	if !conn.Enabled {
		return "", fmt.Errorf("Slack bot is disabled or its token is missing; configure Setup > Connectors > Slack")
	}
	binary, err := exec.LookPath("slack")
	if err != nil {
		for _, candidate := range []string{filepath.Join(os.Getenv("HOME"), ".slack", "bin", "slack"), filepath.Join(os.Getenv("HOME"), ".local", "bin", "slack")} {
			if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() && info.Mode()&0111 != 0 {
				binary = candidate
				break
			}
		}
		if binary == "" {
			return "", fmt.Errorf("Slack CLI is not installed on the backend; install the official Slack CLI")
		}
	}
	return executeSlackCLI(ctx, binary, conn.BotToken, method, parameters)
}

type slackCLIOutput struct{ bytes.Buffer }

func (b *slackCLIOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 2*1024*1024 {
		return 0, fmt.Errorf("Slack response exceeds 2MB; use a smaller page")
	}
	return b.Buffer.Write(p)
}

// slackCLIBodyArgs chooses how parameters reach Slack. Read methods
// (conversations.replies/info/history, users.info, ...) reject a JSON body
// with invalid_arguments, so flat parameters are form-encoded; a JSON body is
// kept only when a parameter is structured (e.g. blocks on a post), which
// only write methods take. (server A 2026-09-25: the QA bot could not read the
// Slack thread it was asked about.)
func slackCLIBodyArgs(parameters map[string]interface{}) ([]string, error) {
	form := url.Values{}
	for key, value := range parameters {
		switch v := value.(type) {
		case nil:
		case string:
			form.Set(key, v)
		case bool:
			form.Set(key, strconv.FormatBool(v))
		case float64:
			form.Set(key, strconv.FormatFloat(v, 'f', -1, 64))
		case int:
			form.Set(key, strconv.Itoa(v))
		case int64:
			form.Set(key, strconv.FormatInt(v, 10))
		case json.Number:
			form.Set(key, v.String())
		default:
			body, err := json.Marshal(parameters)
			if err != nil {
				return nil, fmt.Errorf("invalid Slack API parameters")
			}
			return []string{"--json", string(body)}, nil
		}
	}
	if len(form) == 0 {
		return nil, nil
	}
	return []string{"--data", form.Encode()}, nil
}

func executeSlackCLI(ctx context.Context, binary, token, method string, parameters map[string]interface{}) (string, error) {
	bodyArgs, err := slackCLIBodyArgs(parameters)
	if err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp("", "agentworks-slack-cli-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	args := append([]string{"--skip-update", "--no-color", "--config-dir", dir, "api", method}, bodyArgs...)
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "TMPDIR=" + dir, "SLACK_BOT_TOKEN=" + token}
	var out, stderr slackCLIOutput
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	safe := strings.ReplaceAll(out.String(), token, "[redacted]")
	var response map[string]interface{}
	if json.Unmarshal([]byte(safe), &response) != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("Slack CLI timed out; no automatic retry was attempted")
		}
		if runErr != nil {
			return "", fmt.Errorf("Slack CLI failed before returning an API response; check backend CLI installation and connectivity")
		}
		return "", fmt.Errorf("Slack CLI returned an invalid API response")
	}
	if ok, _ := response["ok"].(bool); !ok {
		code, _ := response["error"].(string)
		needed, _ := response["needed"].(string)
		if code == "missing_scope" {
			return "", fmt.Errorf("Slack permission missing: %s. Add these scopes in OAuth & Permissions, reinstall the app, and save the updated token in Connectors", needed)
		}
		return "", fmt.Errorf("Slack API rejected %s: %s", method, code)
	}
	return safe, nil
}
