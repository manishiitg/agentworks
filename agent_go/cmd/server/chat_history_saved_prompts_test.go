package server

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSavedPromptSizesUseLatestInstructionsWithoutReturningText(t *testing.T) {
	source := []byte(`{"session_id":"s1","conversation_history":[{"Role":"system","Parts":[{"Text":"old instructions"}]},{"role":"developer","parts":[{"text":"developer context"}]},{"Role":"human","Parts":[{"Text":"first question"}]},{"Role":"system","Parts":[{"Text":"latest instructions"}]},{"Role":"human","Parts":[{"Text":"latest question"}]}]}`)
	projected := projectChatHistoryConversationForResumePage(source, 1, 0)
	if strings.Contains(string(projected), "instructions") || strings.Contains(string(projected), "saved_prompt_sizes") {
		t.Fatal("ordinary resume must continue to omit instructions")
	}
	out := attachChatHistorySavedPromptSizes(projected, source)
	var got struct {
		Prompts []struct {
			Role  string `json:"role"`
			Count int    `json:"character_count"`
		} `json:"saved_prompt_sizes"`
		History []json.RawMessage `json:"conversation_history"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Prompts) != 2 || got.Prompts[0].Role != "system" || got.Prompts[0].Count != len("latest instructions") || got.Prompts[1].Count != len("developer context") {
		t.Fatalf("saved prompt sizes = %#v", got.Prompts)
	}
	if strings.Contains(string(out), "latest instructions") || strings.Contains(string(out), "developer context") || strings.Contains(string(out), "saved_prompts") {
		t.Fatal("size response must not return prompt content")
	}
	if len(got.History) != 1 || !strings.Contains(string(got.History[0]), "latest question") {
		t.Fatal("prompt sizes must not change the conversation page")
	}
}

func TestSavedPromptSizesCountFullUnicodeIncludingWhitespace(t *testing.T) {
	text := " \n" + strings.Repeat("界😀", 100000) + "\n "
	source, _ := json.Marshal(map[string]interface{}{"conversation_history": []interface{}{
		map[string]interface{}{"Role": "system", "Parts": []map[string]string{{"Text": text}}},
	}})
	var got struct {
		Prompts []struct {
			Count int `json:"character_count"`
		} `json:"saved_prompt_sizes"`
	}
	out := attachChatHistorySavedPromptSizes([]byte(`{"conversation_history":[],"saved_prompts":[{"text":"obsolete preview"}]}`), source)
	if json.Unmarshal(out, &got) != nil || len(got.Prompts) != 1 || got.Prompts[0].Count != 200004 {
		t.Fatalf("must count the entire prompt in characters, including whitespace: %s", out)
	}
	if len(out) > 1024 || strings.Contains(string(out), "obsolete preview") || strings.Contains(string(out), "界") {
		t.Fatal("only size metadata should cross the network")
	}
	missing := attachChatHistorySavedPromptSizes([]byte(`{"conversation_history":[]}`), []byte(`{"conversation_history":[]}`))
	if !strings.Contains(string(missing), `"saved_prompt_sizes":[]`) {
		t.Fatal("missing prompt must be explicit, never synthesized")
	}
}
