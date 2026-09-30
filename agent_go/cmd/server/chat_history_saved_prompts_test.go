package server

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSavedPromptsUseLatestArchiveInstructionsOutsideChatPagination(t *testing.T) {
	source := []byte(`{"session_id":"s1","conversation_history":[{"Role":"system","Parts":[{"Text":"old instructions"}]},{"role":"developer","parts":[{"text":"developer context"}]},{"Role":"human","Parts":[{"Text":"first question"}]},{"Role":"system","Parts":[{"Text":"latest instructions"}]},{"Role":"human","Parts":[{"Text":"latest question"}]}]}`)
	projected := projectChatHistoryConversationForResumePage(source, 1, 0)
	if strings.Contains(string(projected), "instructions") || strings.Contains(string(projected), "saved_prompts") {
		t.Fatal("ordinary resume must continue to omit instructions")
	}
	var got struct {
		Prompts []struct {
			Role string `json:"role"`
			Text string `json:"text"`
		} `json:"saved_prompts"`
		History []json.RawMessage `json:"conversation_history"`
	}
	if err := json.Unmarshal(attachChatHistorySavedPrompts(projected, source), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Prompts) != 2 || got.Prompts[0].Role != "system" || got.Prompts[0].Text != "latest instructions" || got.Prompts[1].Text != "developer context" {
		t.Fatalf("saved prompts = %#v", got.Prompts)
	}
	if len(got.History) != 1 || !strings.Contains(string(got.History[0]), "latest question") {
		t.Fatal("saved prompt attachment must not change the conversation page")
	}
}

func TestSavedPromptsBoundLargeUTF8AndReportMissingArchive(t *testing.T) {
	text := strings.Repeat("界\"\n", 100000)
	source, _ := json.Marshal(map[string]interface{}{"conversation_history": []interface{}{
		map[string]interface{}{"Role": "system", "Parts": []map[string]string{{"Text": text}}},
		map[string]interface{}{"Role": "developer", "Parts": []map[string]string{{"Text": text}}},
	}})
	var got struct {
		Prompts []struct {
			Text      string `json:"text"`
			Truncated bool   `json:"truncated"`
		} `json:"saved_prompts"`
	}
	out := boundChatHistoryResumeSnapshot(attachChatHistorySavedPrompts([]byte(`{"conversation_history":[]}`), source))
	if len(out) > maxChatHistoryResumeSnapshotBytes || json.Unmarshal(out, &got) != nil || len(got.Prompts) != 2 {
		t.Fatal("large prompts must stay within the response limit")
	}
	for _, prompt := range got.Prompts {
		if !prompt.Truncated || len(prompt.Text) > 64*1024 || !utf8.ValidString(prompt.Text) {
			t.Fatal("truncated prompt must explicitly report a UTF-8-safe preview")
		}
	}
	missing := attachChatHistorySavedPrompts([]byte(`{"conversation_history":[]}`), []byte(`{"conversation_history":[]}`))
	if !strings.Contains(string(missing), `"saved_prompts":[]`) {
		t.Fatal("missing prompt must be explicit, never synthesized")
	}
}
