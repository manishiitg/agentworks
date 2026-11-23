package agycli

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestAgyExecPinsDefaultModelInAPIKeyMode(t *testing.T) {
	argv := agyBuildExecArgv(DefaultModelID, "", "")
	idx := slices.Index(argv, "--model")
	if idx < 0 || idx+1 >= len(argv) || argv[idx+1] != DefaultModelID {
		t.Fatalf("exec argv did not pin the requested model: %v", argv)
	}
}

func TestAgyExecPromptStaysOnStdin(t *testing.T) {
	prompt := strings.Repeat("private text ", 14000)
	argv := agyBuildExecArgv(DefaultModelID, "", "")
	if strings.Contains(strings.Join(argv, " "), prompt[:128]) {
		t.Fatal("prompt leaked into argv")
	}
	input, err := agyStreamInput(prompt)
	if err != nil {
		t.Fatal(err)
	}
	var message struct {
		Event   string `json:"event"`
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(input, &message); err != nil {
		t.Fatal(err)
	}
	if message.Event != "user" || message.Message.Content != prompt {
		t.Fatal("stream input did not preserve the large prompt")
	}
}

func TestAgyStreamExecUsesResultEvent(t *testing.T) {
	output := []byte("{\"event\":\"init\"}\n{\"event\":\"step_update\",\"step_update\":{\"text_delta\":\"draft\"}}\n{\"event\":\"result\",\"result\":{\"status\":\"SUCCESS\",\"response\":\"final\",\"conversation_id\":\"abc\",\"usage\":{\"input_tokens\":3,\"output_tokens\":1}}}\n")
	parsed, err := agyParseStreamExec(output)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.response != "final" || parsed.conversationID != "abc" || parsed.usage.TotalTokens != 4 {
		t.Fatalf("wrong terminal result: %+v", parsed)
	}
}
