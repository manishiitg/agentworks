package services

import (
	"io"
	"mime"
	"mime/multipart"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestBuildGmailMIME(t *testing.T) {
	dir := t.TempDir()
	att := filepath.Join(dir, "report.txt")
	if err := os.WriteFile(att, []byte("hello attachment"), 0o644); err != nil {
		t.Fatal(err)
	}

	raw, err := buildGmailMIME("you@example.com", []string{"cc@example.com"}, "Subject ☂", "body text", "", []string{att})
	if err != nil {
		t.Fatalf("buildGmailMIME: %v", err)
	}
	s := string(raw)

	if !strings.Contains(s, "To: you@example.com\r\n") {
		t.Error("missing To header")
	}
	if !strings.Contains(s, "Cc: cc@example.com\r\n") {
		t.Error("missing Cc header")
	}
	if !strings.Contains(s, "Content-Type: multipart/mixed;") {
		t.Error("missing multipart content-type")
	}
	// Subject with a non-ASCII rune must be RFC 2047 encoded.
	if !strings.Contains(s, "Subject: =?") {
		t.Errorf("subject not encoded: %q", s)
	}

	// Parse the multipart body and confirm a text part + an attachment part.
	_, params, err := mime.ParseMediaType(headerValue(s, "Content-Type"))
	if err != nil {
		t.Fatalf("parse content-type: %v", err)
	}
	body := s[strings.Index(s, "\r\n\r\n")+4:]
	mr := multipart.NewReader(strings.NewReader(body), params["boundary"])
	var sawText, sawAttach bool
	for {
		p, err := mr.NextPart()
		if err != nil {
			break
		}
		if strings.HasPrefix(p.Header.Get("Content-Type"), "text/plain") {
			sawText = true
		}
		if strings.Contains(p.Header.Get("Content-Disposition"), `filename="report.txt"`) {
			sawAttach = true
			if p.Header.Get("Content-Transfer-Encoding") != "base64" {
				t.Error("attachment not base64")
			}
		}
	}
	if !sawText || !sawAttach {
		t.Errorf("expected text+attachment parts, got text=%v attach=%v", sawText, sawAttach)
	}
}

func TestBuildGmailMIMEHTML(t *testing.T) {
	raw, err := buildGmailMIME("you@example.com", nil, "Subj", "plain fallback", "<h1>Hello</h1>", nil)
	if err != nil {
		t.Fatalf("buildGmailMIME html: %v", err)
	}
	s := string(raw)
	if !strings.Contains(s, "text/html; charset=UTF-8") {
		t.Error("missing text/html part")
	}
	if !strings.Contains(s, "<h1>Hello</h1>") {
		t.Error("missing HTML body content")
	}
	if strings.Contains(s, "plain fallback") {
		t.Error("HTML email must not include a plain-text alternative")
	}
}

// headerValue pulls a top-level header value out of the raw message.
func headerValue(msg, key string) string {
	for _, line := range strings.Split(msg, "\r\n") {
		if strings.HasPrefix(line, key+": ") {
			return strings.TrimPrefix(line, key+": ")
		}
		if line == "" {
			break
		}
	}
	return ""
}

func TestGmailSubject(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "   ", "[Agent] Action needed"},
		{"single line", "Approve deploy?", "[Agent] Approve deploy?"},
		{"first line only", "Approve deploy?\nmore detail here", "[Agent] Approve deploy?"},
		{"crlf", "Need input\r\nbody", "[Agent] Need input"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := gmailSubject(tc.in); got != tc.want {
				t.Errorf("gmailSubject(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}

	long := ""
	for i := 0; i < 200; i++ {
		long += "x"
	}
	got := gmailSubject(long)
	if len([]rune(got)) > len("[Agent] ")+151 { // 150 runes + ellipsis
		t.Errorf("gmailSubject did not truncate long input: len=%d", len([]rune(got)))
	}

	// Rune-safe truncation: a long run of multi-byte chars must never be cut
	// mid-rune into invalid UTF-8 (the bug that produced a broken subject char).
	emDashes := strings.Repeat("—", 200) // 3 bytes each
	if s := gmailSubject(emDashes); !utf8.ValidString(s) {
		t.Errorf("gmailSubject produced invalid UTF-8 on multi-byte input: %q", s)
	}
}

func TestRenderGmailButtonOptions(t *testing.T) {
	if got := renderGmailButtonOptions(nil); got != "" {
		t.Errorf("nil opts = %q, want empty", got)
	}
	if got := renderGmailButtonOptions(&ButtonOptions{YesNoOnly: true}); got != "Options: Approve / Reject" {
		t.Errorf("default yes/no = %q", got)
	}
	if got := renderGmailButtonOptions(&ButtonOptions{YesNoOnly: true, YesLabel: "Ship", NoLabel: "Hold"}); got != "Options: Ship / Hold" {
		t.Errorf("custom yes/no = %q", got)
	}
	if got := renderGmailButtonOptions(&ButtonOptions{Options: []string{"A", "B", "C"}}); got != "Options: A / B / C" {
		t.Errorf("options = %q", got)
	}
}

func TestParseGwsMessageID(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", "sent"},
		{"id field", `{"id":"abc123"}`, "abc123"},
		{"messageId field", `{"messageId":"m_99"}`, "m_99"},
		{"threadId fallback", `{"threadId":"t_7"}`, "t_7"},
		{"non-json", "Message sent.", "sent"},
		{"json no id", `{"status":"ok"}`, "sent"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseGwsMessageID([]byte(tc.in)); got != tc.want {
				t.Errorf("parseGwsMessageID(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestGmailPickRecipient(t *testing.T) {
	g := &GmailService{
		defaultTo: "fallback@example.com",
		config: &GmailConfig{
			DefaultTo:         "fallback@example.com",
			BlockedRecipients: []string{"blocked@example.com"},
		},
	}

	// explicit hint wins
	dest := &NotificationDestination{Gmail: &GmailDest{Email: "hint@example.com"}}
	if got := g.pickRecipient(dest); got != "hint@example.com" {
		t.Fatalf("explicit hint = %q, want hint@example.com", got)
	}

	// explicit To override may contain more than one recipient
	if got := g.pickRecipient(&NotificationDestination{Gmail: &GmailDest{Email: "Hint@Example.com, ops@example.com"}}); got != "hint@example.com, ops@example.com" {
		t.Fatalf("multi-recipient explicit hint = %q, want two recipients", got)
	}

	// A blocked address is dropped, and the rest of the list still receives the
	// mail. Failing the whole send here meant one denylisted colleague silenced
	// the notification for everyone else on it.
	if got := g.pickRecipient(&NotificationDestination{Gmail: &GmailDest{Email: "ops@example.com, blocked@example.com, lead@example.com"}}); got != "ops@example.com, lead@example.com" {
		t.Fatalf("mixed list = %q, want the blocked address dropped and the rest kept", got)
	}

	// When every resolved recipient is blocked there is genuinely nowhere to
	// send, which is a silent skip rather than an error.
	if got := g.pickRecipient(&NotificationDestination{Gmail: &GmailDest{Email: "blocked@example.com"}}); got != "" {
		t.Fatalf("fully blocked hint = %q, want empty (skip)", got)
	}

	// A per-workflow denylist filters the same way, without dropping the others.
	perWorkflow := &NotificationDestination{Gmail: &GmailDest{
		Email:             "ops@example.com, lead@example.com",
		BlockedRecipients: []string{"lead@example.com"},
	}}
	if got := g.pickRecipient(perWorkflow); got != "ops@example.com" {
		t.Fatalf("per-workflow denylist = %q, want only the allowed recipient", got)
	}

	// no hint, no user -> workspace default
	if got := g.pickRecipient(nil); got != "fallback@example.com" {
		t.Errorf("default = %q, want fallback@example.com", got)
	}

	// disabled service still resolves recipient via fields (enablement gates at SendNotification)
	if got := g.pickRecipient(&NotificationDestination{}); got != "fallback@example.com" {
		t.Errorf("empty dest = %q, want fallback@example.com", got)
	}
}

func TestGmailValidateCCRecipients(t *testing.T) {
	g := &GmailService{
		defaultTo: "fallback@example.com",
		config: &GmailConfig{
			DefaultTo:         "fallback@example.com",
			BlockedRecipients: []string{"blocked@example.com"},
		},
	}

	got := g.filterCCRecipients([]string{" CC@Example.com ", "other@example.com,cc@example.com"})
	want := []string{"cc@example.com", "other@example.com"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("filterCCRecipients = %#v, want %#v", got, want)
	}

	// A blocked CC is removed; the send continues to everyone else.
	if got := g.filterCCRecipients([]string{"cc@example.com", "blocked@example.com"}); !reflect.DeepEqual(got, []string{"cc@example.com"}) {
		t.Fatalf("mixed cc = %#v, want the blocked address dropped", got)
	}
	if got := g.filterCCRecipients([]string{"blocked@example.com"}); len(got) != 0 {
		t.Fatalf("fully blocked cc = %#v, want no CC recipients", got)
	}
}

func TestFilterRecipientsAgainstBlockedList(t *testing.T) {
	got, dropped := filterRecipientsAgainstList(
		[]string{"User@Example.com, ops@example.com"},
		[]string{"blocked@example.com"},
	)
	want := []string{"user@example.com", "ops@example.com"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("allowed = %#v, want %#v", got, want)
	}
	if len(dropped) != 0 {
		t.Fatalf("dropped = %#v, want none", dropped)
	}

	// Matching is case-insensitive on both sides, and only the blocked entry goes.
	got, dropped = filterRecipientsAgainstList([]string{"Ops@Example.com, blocked@example.com"}, []string{"BLOCKED@example.com"})
	if !reflect.DeepEqual(got, []string{"ops@example.com"}) {
		t.Fatalf("allowed = %#v, want only ops@example.com", got)
	}
	if !reflect.DeepEqual(dropped, []string{"blocked@example.com"}) {
		t.Fatalf("dropped = %#v, want the blocked address reported", dropped)
	}
}

func TestNormalizeGmailConfigDedupesBlockedRecipients(t *testing.T) {
	cfg := normalizeGmailConfig(&GmailConfig{
		DefaultTo:         " Owner@Example.COM ",
		BlockedRecipients: []string{"other@example.com", "owner@example.com", "a@b.com, C@D.com", "other@example.com"},
	})

	wantBlocked := []string{"other@example.com", "owner@example.com", "a@b.com", "c@d.com"}
	if cfg.DefaultTo != "Owner@Example.COM" {
		t.Fatalf("DefaultTo = %q, want trimmed original case", cfg.DefaultTo)
	}
	if !reflect.DeepEqual(cfg.BlockedRecipients, wantBlocked) {
		t.Fatalf("BlockedRecipients = %#v, want %#v", cfg.BlockedRecipients, wantBlocked)
	}
}

func TestGmailDenylistAllowsRecipientsByDefault(t *testing.T) {
	g := &GmailService{defaultTo: "fallback@example.com"}

	if got := g.pickRecipient(nil); got != "fallback@example.com" {
		t.Fatalf("default = %q, want fallback@example.com", got)
	}

	if got := g.pickRecipient(&NotificationDestination{Gmail: &GmailDest{Email: "hint@example.com"}}); got != "hint@example.com" {
		t.Fatalf("hint = %q, want hint@example.com", got)
	}
}

// The notification settings popup reads this on every open. `gws auth status`
// spawns a Node CLI and takes ~5.5s, so a synchronous read left the popup on a
// spinner for that whole time even though every other field it needs is already
// in memory. A cold read must return immediately and report Checking.
func TestAuthStatusCachedNeverBlocksOnAColdRead(t *testing.T) {
	g := &GmailService{gwsPath: "definitely-not-a-real-binary-xyz"}

	start := time.Now()
	st := g.AuthStatusCached()
	elapsed := time.Since(start)

	if !st.Checking {
		t.Fatalf("cold read should report Checking, got %#v", st)
	}
	if elapsed > time.Second {
		t.Fatalf("cold read took %v — it must not wait on the subprocess", elapsed)
	}
}

// A fresh cached result is served as-is, which is the whole point: opening the
// popup twice must not pay for two subprocesses.
func TestAuthStatusCachedServesFreshCache(t *testing.T) {
	g := &GmailService{
		authCaches: map[string]*gmailAuthCacheEntry{
			"": {
				status:   &GmailAuthStatus{Authenticated: true, HasGmailScope: true, GwsInstalled: true},
				cachedAt: time.Now(),
			},
		},
	}
	st := g.AuthStatusCached()
	if !st.Authenticated || !st.HasGmailScope || st.Checking {
		t.Fatalf("cached status not served: %#v", st)
	}
}

// A stale cache still beats a pending badge: report the last known answer while
// the refresh runs, rather than flipping the UI back to "checking".
func TestAuthStatusCachedPrefersStaleOverPending(t *testing.T) {
	g := &GmailService{
		gwsPath: "definitely-not-a-real-binary-xyz",
		authCaches: map[string]*gmailAuthCacheEntry{
			"": {
				status:   &GmailAuthStatus{Authenticated: true, HasGmailScope: true},
				cachedAt: time.Now().Add(-2 * gmailAuthCacheTTL),
			},
		},
	}
	st := g.AuthStatusCached()
	if st.Checking {
		t.Fatal("a known previous answer should be shown while refreshing")
	}
	if !st.Authenticated {
		t.Fatalf("stale status lost: %#v", st)
	}
}

func TestGmailRichBodyContainsOnlyHTML(t *testing.T) {
	raw, err := buildGmailMIME("user@example.com", nil, "Autopilot", "PLAIN_ONLY fallback", "<p>RICH_ONLY summary</p>", nil)
	if err != nil {
		t.Fatal(err)
	}
	message := string(raw)
	_, params, err := mime.ParseMediaType(headerValue(message, "Content-Type"))
	if err != nil {
		t.Fatal(err)
	}
	outer := multipart.NewReader(strings.NewReader(message[strings.Index(message, "\r\n\r\n")+4:]), params["boundary"])
	part, err := outer.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	kind, _, err := mime.ParseMediaType(part.Header.Get("Content-Type"))
	if err != nil || kind != "text/html" {
		t.Fatalf("expected a single HTML body, got %q", kind)
	}
	body, _ := io.ReadAll(part)
	if string(body) != "<p>RICH_ONLY summary</p>" {
		t.Fatalf("unexpected HTML body %q", string(body))
	}
	if strings.Contains(message, "PLAIN_ONLY fallback") {
		t.Fatal("plain fallback was included in rich email")
	}
	if _, err := outer.NextPart(); err != io.EOF {
		t.Fatal("extra visible body part")
	}
}
