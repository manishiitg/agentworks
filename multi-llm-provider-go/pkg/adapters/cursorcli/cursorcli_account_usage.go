package cursorcli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// ReadAccountUsage uses the selected CLI login. API keys cannot read the
// dashboard quota and must never fall back to another saved login.
func ReadAccountUsage(ctx context.Context, environment []string) (llmtypes.AccountUsage, error) {
	result := llmtypes.AccountUsage{CheckedAt: time.Now().UTC()}
	token, err := cursorUsageToken(environment)
	if err != nil {
		return result, err
	}
	if token == "" {
		return result, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api2.cursor.sh/aiserver.v1.DashboardService/GetCurrentPeriodUsage", strings.NewReader("{}"))
	if err != nil {
		return result, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("connect-protocol-version", "1")
	request.Header.Set("x-cursor-client-type", "cli")
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return result, llmtypes.AccountUsageFailure("request_failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return result, llmtypes.AccountUsageFailure(fmt.Sprintf("http_%d", response.StatusCode))
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 64*1024))
	if err != nil {
		return result, llmtypes.AccountUsageFailure("request_failed")
	}
	return parseCursorAccountUsage(raw, result)
}
func cursorUsageToken(environment []string) (string, error) {
	env := map[string]string{}
	for _, entry := range environment {
		if k, v, ok := strings.Cut(entry, "="); ok {
			env[k] = v
		}
	}
	token := strings.TrimSpace(env["CURSOR_AUTH_TOKEN"])
	if token != "" {
		return token, nil
	}
	if strings.TrimSpace(env["CURSOR_API_KEY"]) != "" || env["AGENT_CLI_CREDENTIAL_STORE"] == "memory" {
		return "", nil
	}
	// Keychain access is not inferred from a different account's HOME.
	if runtime.GOOS == "darwin" && env["AGENT_CLI_CREDENTIAL_STORE"] != "file" {
		return "", nil
	}
	home := env["HOME"]
	if runtime.GOOS == "windows" {
		home = env["USERPROFILE"]
	}
	if home == "" {
		return "", nil
	}
	dir := filepath.Join(home, ".cursor")
	if runtime.GOOS == "linux" {
		base := env["XDG_CONFIG_HOME"]
		if base == "" {
			base = filepath.Join(home, ".config")
		}
		dir = filepath.Join(base, "cursor")
	}
	if runtime.GOOS == "windows" {
		base := env["APPDATA"]
		if base == "" {
			base = filepath.Join(home, "AppData", "Roaming")
		}
		dir = filepath.Join(base, "Cursor")
	}
	f, err := os.Open(filepath.Join(dir, "auth.json"))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", llmtypes.AccountUsageFailure("login_unavailable")
	}
	defer f.Close()
	var credential struct {
		AccessToken string `json:"accessToken"`
	}
	if json.NewDecoder(io.LimitReader(f, 64*1024)).Decode(&credential) != nil {
		return "", llmtypes.AccountUsageFailure("login_unreadable")
	}
	return strings.TrimSpace(credential.AccessToken), nil
}
func parseCursorAccountUsage(raw []byte, result llmtypes.AccountUsage) (llmtypes.AccountUsage, error) {
	var response struct {
		BillingCycleEnd json.Number         `json:"billingCycleEnd"`
		PlanUsage       map[string]*float64 `json:"planUsage"`
	}
	if json.Unmarshal(raw, &response) != nil {
		return result, llmtypes.AccountUsageFailure("unreadable")
	}
	var reset time.Time
	if millis, err := response.BillingCycleEnd.Int64(); err == nil && millis > 0 {
		reset = time.UnixMilli(millis)
	}
	for _, w := range []struct{ key, label string }{{"totalPercentUsed", "Monthly total"}, {"autoPercentUsed", "Monthly Auto"}, {"apiPercentUsed", "Monthly API"}} {
		result.AddWindow(w.label, response.PlanUsage[w.key], reset)
	}
	return result, nil
}
