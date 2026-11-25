package agycli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// An Antigravity chat said "This account has not reported its plan use yet": nothing asked Google for the
// account's quota. The answer's shape is the one Google returned for a consumer sign-in (25 Nov 2026).
func TestAgyAccountUsageReadsTheQuotaSummary(t *testing.T) {
	requested := time.Now().UTC()
	untouchedReset := requested.Add(5 * time.Hour).Format(time.RFC3339)
	summary := `{"description":"x","groups":[
	 {"displayName":"Gemini Models","buckets":[
	  {"bucketId":"gemini-weekly","window":"weekly","description":"d","remainingFraction":0.81,"resetTime":"2026-12-02T03:25:04Z"},
	  {"bucketId":"gemini-5h","window":"5h","description":"d","remainingFraction":0.66,"resetTime":"2026-11-25T13:25:04Z"}]},
	 {"displayName":"Claude and GPT models","buckets":[
	  {"bucketId":"3p-5h","window":"5h","remainingFraction":1,"resetTime":"` + untouchedReset + `"}]}]}`
	var auth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(summary))
	}))
	defer server.Close()
	original := agyQuotaEndpoints
	agyQuotaEndpoints = map[bool][]string{true: {server.URL}, false: {server.URL}}
	defer func() { agyQuotaEndpoints = original }()

	home := t.TempDir()
	file := agyLoginFile(home)
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	login := `{"token":{"access_token":"example-token","expiry":"` + time.Now().Add(time.Hour).Format(time.RFC3339) + `"},"auth_method":"consumer"}`
	if err := os.WriteFile(file, []byte(login), 0o600); err != nil {
		t.Fatal(err)
	}
	usage, err := ReadAccountUsage(context.Background(), []string{"HOME=" + home})
	if err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer example-token" {
		t.Fatalf("the account's own sign-in must be used, got %q", auth)
	}
	if len(usage.Windows) != 3 || usage.Windows[0].Label != "Gemini · 5-hour limit" && usage.Windows[0].Label != "Gemini · Weekly limit" {
		t.Fatalf("Gemini windows come first: %+v", usage.Windows)
	}
	for _, w := range usage.Windows {
		switch w.Label {
		case "Gemini · 5-hour limit":
			if w.UsedPercent < 33.9 || w.UsedPercent > 34.1 || w.ResetsAt == nil {
				t.Fatalf("5-hour window: %+v", w)
			}
		case "Claude & GPT · 5-hour limit":
			if w.UsedPercent != 0 || w.ResetsAt != nil {
				t.Fatalf("an untouched window has no reset time yet: %+v", w)
			}
		}
	}

	// An expired sign-in is not used; agy refreshes it on its next run.
	expired := `{"token":{"access_token":"example-token","expiry":"` + time.Now().Add(-time.Hour).Format(time.RFC3339) + `"}}`
	if err := os.WriteFile(file, []byte(expired), 0o600); err != nil {
		t.Fatal(err)
	}
	auth = ""
	if _, err := ReadAccountUsage(context.Background(), []string{"HOME=" + home}); err == nil || auth != "" {
		t.Fatalf("an expired sign-in must not be sent (err=%v)", err)
	}
}
