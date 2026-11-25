package agycli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Antigravity meters consumer sign-ins on the daily host and business ones on prod; both are asked and the
// metered host's reading of a bucket wins (the same order T3 Code uses).
var agyQuotaEndpoints = map[bool][]string{
	true:  {"https://daily-cloudcode-pa.googleapis.com", "https://cloudcode-pa.googleapis.com"},
	false: {"https://cloudcode-pa.googleapis.com", "https://daily-cloudcode-pa.googleapis.com"},
}

var agyQuotaClient = &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// agyLoginFile is where agy keeps an account's Google sign-in when it runs with file storage (agyAccountEnv).
func agyLoginFile(home string) string {
	return filepath.Join(home, ".gemini", "antigravity-cli", "antigravity-oauth-token")
}

// ReadAccountUsage asks Google for the plan use of the account whose HOME is in environment: the 5-hour and
// weekly windows of each model group. It uses the sign-in agy saved for that account while it is valid (agy
// refreshes it as it runs) and never changes it. An account without a saved Google sign-in reports nothing.
func ReadAccountUsage(ctx context.Context, environment []string) (llmtypes.AccountUsage, error) {
	result := llmtypes.AccountUsage{CheckedAt: time.Now().UTC()}
	home := ""
	for _, entry := range environment {
		if value, ok := strings.CutPrefix(entry, "HOME="); ok {
			home = value
		}
	}
	if home == "" {
		return result, llmtypes.AccountUsageFailure("no_account_home")
	}
	raw, err := os.ReadFile(agyLoginFile(home))
	if err != nil {
		return result, llmtypes.AccountUsageFailure("not_signed_in")
	}
	var login struct {
		Token struct {
			AccessToken string    `json:"access_token"`
			Expiry      time.Time `json:"expiry"`
		} `json:"token"`
		AuthMethod string `json:"auth_method"`
	}
	if json.Unmarshal(raw, &login) != nil || login.Token.AccessToken == "" {
		return result, llmtypes.AccountUsageFailure("unreadable")
	}
	if !login.Token.Expiry.IsZero() && time.Now().After(login.Token.Expiry.Add(-time.Minute)) {
		return result, llmtypes.AccountUsageFailure("sign_in_expired")
	}
	consumer := login.AuthMethod == "" || login.AuthMethod == "consumer" || login.AuthMethod == "oauth-personal"
	seen := map[string]bool{}
	asked := false
	for _, endpoint := range agyQuotaEndpoints[consumer] {
		body, requestedAt, ok := agyQuotaRequest(ctx, endpoint, login.Token.AccessToken)
		if !ok {
			continue
		}
		asked = true
		for _, window := range agySummaryWindows(body, requestedAt) {
			if seen[window.label] {
				continue
			}
			seen[window.label] = true
			percent := window.used
			result.AddWindow(window.label, &percent, window.resets)
		}
	}
	if !asked {
		return result, llmtypes.AccountUsageFailure("probe_failed")
	}
	return result, nil
}

func agyQuotaRequest(ctx context.Context, endpoint, token string) ([]byte, time.Time, bool) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/v1internal:retrieveUserQuotaSummary", bytes.NewReader([]byte("{}")))
	if err != nil {
		return nil, time.Time{}, false
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "antigravity/hub/2.24.0 (aidev_client; os_type="+runtime.GOOS+"; arch="+runtime.GOARCH+")")
	requestedAt := time.Now()
	response, err := agyQuotaClient.Do(request)
	if err != nil {
		return nil, requestedAt, false
	}
	defer response.Body.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(io.LimitReader(response.Body, 1<<20)); err != nil || response.StatusCode != http.StatusOK {
		return nil, requestedAt, false
	}
	return buf.Bytes(), requestedAt, true
}

type agyQuotaWindow struct {
	label  string
	used   float64
	resets time.Time
}

// agySummaryWindows reads a retrieveUserQuotaSummary answer. Gemini comes first so a chat's plan line, which
// keeps the first 5-hour and weekly window, shows the default models' allowance.
func agySummaryWindows(raw []byte, requestedAt time.Time) []agyQuotaWindow {
	type bucket struct {
		BucketID          string   `json:"bucketId"`
		Window            string   `json:"window"`
		Description       *string  `json:"description"`
		RemainingFraction *float64 `json:"remainingFraction"`
		ResetTime         string   `json:"resetTime"`
		Disabled          bool     `json:"disabled"`
	}
	var summary struct {
		Groups []struct {
			DisplayName string   `json:"displayName"`
			Buckets     []bucket `json:"buckets"`
		} `json:"groups"`
		Buckets []bucket `json:"buckets"`
	}
	if json.Unmarshal(raw, &summary) != nil {
		return nil
	}
	groups := summary.Groups
	if len(groups) == 0 {
		groups = append(groups, struct {
			DisplayName string   `json:"displayName"`
			Buckets     []bucket `json:"buckets"`
		}{Buckets: summary.Buckets})
	}
	var gemini, others []agyQuotaWindow
	for _, group := range groups {
		for _, b := range group.Buckets {
			if b.Disabled || b.RemainingFraction == nil || math.IsNaN(*b.RemainingFraction) {
				continue
			}
			var minutes int
			label := ""
			switch b.Window {
			case "5h":
				minutes, label = 5*60, "5-hour limit"
			case "weekly":
				minutes, label = 7*24*60, "Weekly limit"
			default:
				continue
			}
			family := strings.TrimSpace(group.DisplayName)
			switch {
			case strings.HasPrefix(b.BucketID, "gemini-argon-"):
				family = firstNonEmpty(family, "Gemini Argon")
			case strings.HasPrefix(b.BucketID, "gemini-carbon-"):
				family = firstNonEmpty(family, "Gemini Carbon")
			case strings.HasPrefix(b.BucketID, "gemini-"):
				family = "Gemini"
			case strings.HasPrefix(b.BucketID, "3p-"):
				family = "Claude & GPT"
			}
			family = firstNonEmpty(family, b.BucketID)
			remaining := math.Max(0, math.Min(1, *b.RemainingFraction))
			reset, _ := time.Parse(time.RFC3339, b.ResetTime)
			// An untouched bucket reports a rolling full-window reset; it has no real reset time yet.
			if remaining == 1 && b.Description == nil && !reset.IsZero() && math.Abs(reset.Sub(requestedAt).Minutes()-float64(minutes)) <= 1 {
				reset = time.Time{}
			}
			window := agyQuotaWindow{label: family + " · " + label, used: (1 - remaining) * 100, resets: reset}
			if family == "Gemini" {
				gemini = append(gemini, window)
			} else {
				others = append(others, window)
			}
		}
	}
	return append(gemini, others...)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
