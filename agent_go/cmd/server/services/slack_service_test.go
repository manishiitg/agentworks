package services

import (
	"context"
	"encoding/json"
	"github.com/manishiitg/mcpagent/events"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSlackUserNotificationDestinationRequiresExplicitSlackDestination(t *testing.T) {
	svc := &SlackService{}

	channelID, threadTS := svc.pickUserNotificationDestination(nil)
	if channelID != "" || threadTS != "" {
		t.Fatalf("user notification destination without Slack hint = %q/%q, want empty", channelID, threadTS)
	}

	channelID, threadTS = svc.pickUserNotificationDestination(&NotificationDestination{})
	if channelID != "" || threadTS != "" {
		t.Fatalf("user notification destination without Slack hint = %q/%q, want empty", channelID, threadTS)
	}

	channelID, threadTS = svc.pickUserNotificationDestination(&NotificationDestination{
		Slack: &SlackDest{ChannelID: "C123", ThreadTS: "171.1"},
	})
	if channelID != "C123" || threadTS != "171.1" {
		t.Fatalf("user notification destination = %q/%q, want C123/171.1", channelID, threadTS)
	}
}

func TestSlackFeedbackNotificationHasNoDefaultFallback(t *testing.T) {
	svc := &SlackService{}

	channelID, threadTS := svc.pickDestination(nil)
	if channelID != "" || threadTS != "" {
		t.Fatalf("feedback notification destination = %q/%q, want empty/empty", channelID, threadTS)
	}
}

func TestSlackConnectionDoesNotRequireDefaultChannel(t *testing.T) {
	svc := &SlackService{enabled: true, client: slack.New("test")}
	if !svc.IsEnabled() {
		t.Fatal("connector requires a legacy channel")
	}
	var cfg SlackConfig
	if err := json.Unmarshal([]byte(`{"enabled":true,"channel_id":"CLEGACY"}`), &cfg); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "channel_id") {
		t.Fatal("legacy default channel survived serialization")
	}
}

func TestSlackMessageTagsAnotherUser(t *testing.T) {
	cases := []struct {
		name string
		text string
		bot  string
		want bool
	}{
		{"plain reply", "looks good, ship it", "UBOT", false},
		{"bot tag only", "<@UBOT> run the report", "UBOT", false},
		{"other user tag", "<@U123> can you take a look?", "UBOT", true},
		{"bot plus other user", "<@UBOT> ask <@U123> to rerun", "UBOT", true},
		{"labeled tag", "ping <@U123|bob> about it", "UBOT", true},
		{"channel ping is not a person", "<!channel> heads up", "UBOT", false},
		{"here ping is not a person", "<!here> standup time", "UBOT", false},
		{"unknown bot id fails open", "<@U123> hi", "", false},
	}
	for _, tc := range cases {
		if got := slackMessageTagsAnotherUser(tc.text, tc.bot); got != tc.want {
			t.Errorf("%s: slackMessageTagsAnotherUser(%q) = %v, want %v", tc.name, tc.text, got, tc.want)
		}
	}
}

func TestRewriteMentionTagsResolvesNames(t *testing.T) {
	resolve := func(userID string) string {
		if userID == "U123" {
			return "bob"
		}
		return ""
	}
	cases := []struct {
		name string
		text string
		want string
	}{
		{"no tags", "run the report", "run the report"},
		{"known user", "ask <@U123> to rerun", "ask @bob to rerun"},
		{"labeled tag", "ping <@U123|robert> now", "ping @bob now"},
		{"unknown user dropped", "ask <@U999> today", "ask  today"},
		{"channel ping untouched", "<!here> standup", "<!here> standup"},
	}
	for _, tc := range cases {
		if got := rewriteMentionTags(tc.text, resolve); got != tc.want {
			t.Errorf("%s: rewriteMentionTags(%q) = %q, want %q", tc.name, tc.text, got, tc.want)
		}
	}
}

// A top-level @mention with an attachment: slackevents.AppMentionEvent drops
// the files, so they are read from the raw event (PLAT-668).
func TestSlackMentionFilesFromRawEvent(t *testing.T) {
	evt := socketmode.Event{Request: &socketmode.Request{Payload: []byte(`{"event":{"type":"app_mention","text":"<@U1> see","files":[{"id":"F1","name":"report.csv"}]}}`)}}
	raw := slackMentionFiles(evt)
	if raw == nil || len(raw.Files) != 1 || raw.Files[0].ID != "F1" {
		t.Fatalf("files = %+v", raw)
	}
	if slackMentionFiles(socketmode.Event{Request: &socketmode.Request{Payload: []byte(`{"event":{"type":"app_mention"}}`)}}) != nil {
		t.Fatal("a mention without files produced some")
	}
}

// PLAT-750-89: exercise the actual Node bundle and Go Slack transport. Only
// Slack's HTTP boundary is replaced; it must never receive model JSON fences.
func TestSlackVisualReplyStreamsNativeBlocksAndFinalAuthority(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatal("Node is required for Slack formatting qualification")
	}
	var mu sync.Mutex
	var requests []url.Values
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		mu.Lock()
		requests = append(requests, r.PostForm)
		mu.Unlock()
		blocks := json.RawMessage(r.PostForm.Get("blocks"))
		if len(blocks) == 0 {
			blocks = json.RawMessage("[]")
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "channel": "CTEST", "ts": "123.002", "message": map[string]interface{}{"text": r.PostForm.Get("text"), "blocks": blocks}})
	}))
	defer api.Close()
	svc := &SlackService{client: slack.New("test", slack.OptionAPIURL(api.URL+"/"))}
	thread := ThreadID{Platform: "slack", ChannelID: "CTEST", ThreadTS: "123.001"}
	f := NewBotEventFilter(svc, thread, "session", "", "person")
	op := func(path string, value interface{}) string {
		b, _ := json.Marshal(map[string]interface{}{"op": "add", "path": path, "value": value})
		return string(b)
	}
	start := "Saved total is 100.\n```chat-ui\n" + op("/root", "visual") + "\n" + op("/elements/visual", map[string]interface{}{"type": "chart", "props": map[string]interface{}{"title": "Requests", "style": "line", "data": []interface{}{}}, "children": []string{}}) + "\n"
	pointPrefix := `{"op":"add","path":"/elements/visual/props/data/-","value":{"label":"Mon (MCP)","value":1e`
	first := start + pointPrefix + "2}}\n"
	push := func(text string) {
		event := replyChunk(text, true)
		event.Data.Data.(*events.StreamingChunkEvent).Source = events.StreamingChunkSourceContent
		f.sendStreamingChunk(context.Background(), event)
	}
	push(start + pointPrefix)
	mu.Lock()
	if len(requests) != 1 {
		t.Fatalf("first placeholder posts=%d", len(requests))
	}
	initial := requests[0]
	mu.Unlock()
	if strings.Contains(initial.Get("text"), `"op"`) || strings.Contains(initial.Get("blocks"), "chat-ui") {
		t.Fatal("incomplete visual exposed source")
	}
	// The rest of the exponent arrives in a separate native delta. Literal
	// user labels such as (MCP) must not be mistaken for CLI status lines.
	f.mu.Lock()
	f.lastStreamingUpdate = time.Time{}
	f.mu.Unlock()
	push("2}}\n")
	mu.Lock()
	if len(requests) != 2 {
		t.Fatalf("first chart requests=%d", len(requests))
	}
	chart := requests[1]
	mu.Unlock()
	if !strings.Contains(chart.Get("blocks"), `"data_visualization"`) || strings.Contains(chart.Get("blocks"), `"image_url"`) {
		t.Fatalf("chart is not a local native Slack chart: %s", chart.Get("blocks"))
	}
	if chart.Get("ts") != "123.002" || initial.Get("thread_ts") != "123.001" {
		t.Fatal("reply escaped its message/thread")
	}
	f.mu.Lock()
	f.lastStreamingUpdate = time.Now()
	f.mu.Unlock()
	push(op("/elements/visual/props/data/-", map[string]interface{}{"label": "Tue", "value": 20}) + "\n")
	mu.Lock()
	if len(requests) != 2 {
		t.Fatal("throttled edit sent immediately")
	}
	mu.Unlock()
	// Even an identical already-sent source must receive strict final validation.
	if !f.flushStreamingMessage(context.Background(), first) {
		t.Fatal("final edit not delivered")
	}
	time.Sleep(botStreamUpdateInterval + 100*time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 3 {
		t.Fatalf("late partial edit or missing final: %d", len(requests))
	}
	final := requests[2]
	if final.Get("ts") != "123.002" || strings.Contains(final.Get("blocks"), "Tue") || strings.Contains(final.Get("blocks"), `"op"`) {
		t.Fatal("canonical final lost to partial text/source")
	}
}

func TestSlackVisualReplyTablesCardsAndPlainFallback(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatal("Node is required for Slack formatting qualification")
	}
	var forms []url.Values
	rejectNext := false
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		forms = append(forms, r.PostForm)
		if rejectNext {
			rejectNext = false
			_, _ = w.Write([]byte(`{"ok":false,"error":"invalid_blocks"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"channel":"CTEST","ts":"123.002"}`))
	}))
	defer api.Close()
	svc := &SlackService{client: slack.New("test", slack.OptionAPIURL(api.URL+"/"))}
	thread := ThreadID{Platform: "slack", ChannelID: "CTEST", ThreadTS: "123.001"}
	makeReply := func(kind string, props map[string]interface{}) string {
		root, _ := json.Marshal(map[string]interface{}{"op": "add", "path": "/root", "value": "visual"})
		widget, _ := json.Marshal(map[string]interface{}{"op": "add", "path": "/elements/visual", "value": map[string]interface{}{"type": kind, "props": props, "children": []string{}}})
		return "Saved total is 12.\n```chat-ui\n" + string(root) + "\n" + string(widget) + "\n```"
	}
	// Windows-style reply line endings must still hide visual source.
	table := strings.ReplaceAll(makeReply("table", map[string]interface{}{"title": "Totals", "headers": []string{"Day", "Total"}, "rows": [][]interface{}{{"Monday", 12}}}), "\n", "\r\n")
	if _, err := svc.SendThreadMessage(context.Background(), thread, table); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(forms[0].Get("blocks"), `"type":"data_table"`) || strings.Contains(forms[0].Get("text"), `"op"`) {
		t.Fatal("table was not rendered natively")
	}
	cards := makeReply("cards", map[string]interface{}{"title": "Options", "items": []interface{}{map[string]interface{}{"title": "Saved report", "description": "A useful result", "href": "https://example.com/report"}}})
	if err := svc.UpdateMessage(context.Background(), thread, "123.002", cards); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(forms[1].Get("blocks"), "Saved report") || strings.Contains(forms[1].Get("blocks"), "chat-ui") {
		t.Fatal("card leaked its spec")
	}
	if _, err := svc.WriteReply(context.Background(), ThreadID{Platform: "slack", ChannelID: "CTEST", ConnectionID: "missing"}, "", cards, true); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatal("unknown connection was not refused by routing")
	}
	if len(forms) != 2 {
		t.Fatal("unknown connection reached Slack")
	}
	// A workspace that rejects newer native blocks still gets the saved values.
	rejectNext = true
	if _, err := svc.WriteReply(context.Background(), thread, "123.002", table, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(forms[3].Get("text"), "Monday") || strings.Contains(forms[3].Get("text"), `"op"`) {
		t.Fatal("rejected blocks lost the data or exposed source")
	}
	forms = forms[:2]
	t.Setenv("PATH", t.TempDir())
	if _, err := svc.WriteReply(context.Background(), thread, "", table, true); err != nil {
		t.Fatal(err)
	}
	fallback := forms[2]
	if !strings.Contains(fallback.Get("text"), "Saved total is 12") || !strings.Contains(fallback.Get("text"), "text summary") || strings.Contains(fallback.Get("blocks"), `"op"`) {
		t.Fatal("missing Node lost the plain answer or exposed JSON")
	}
	long := strings.Repeat("Saved result. ", 300)
	if err := svc.UpdateMessage(context.Background(), thread, "123.002", long); err != nil {
		t.Fatal(err)
	}
	if forms[3].Get("blocks") != "[]" {
		t.Fatal("a final plain-text reply retained stale native blocks")
	}
}

func TestSlackVisualSourceKeepsWorkspacePathsOutsideProseFormatting(t *testing.T) {
	f := NewBotEventFilter(nil, ThreadID{Platform: "slack"}, "session", "https://example.com", "person")
	source := "Open Chats/sample/report.md\n```chat-ui\n{\"image\":\"Chats/sample/photo.png\"}\n```"
	rewritten := f.replaceWorkspacePaths(source)
	if !strings.Contains(rewritten, "https://example.com/file?") || !strings.Contains(rewritten, `"image":"Chats/sample/photo.png"`) {
		t.Fatal("prose link mapping rewrote visual data")
	}
}
