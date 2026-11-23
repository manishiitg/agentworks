package cursorcli

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"

	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

var codingCLIStress = flag.Bool("coding-cli-stress", false, "run the opt-in live native-tools stress scenario")

// readCursorRetainedInputAll returns the text of every assistant message in
// the store's latest root.
func readCursorRetainedInputAll(storeDB string) []string {
	db, err := sql.Open("sqlite", "file:"+storeDB+"?mode=ro")
	if err != nil {
		return nil
	}
	defer db.Close()
	ctx := context.Background()
	refs, err := cursorStoreLatestRootRefs(ctx, db)
	if err != nil {
		return nil
	}
	var texts []string
	for _, ref := range refs {
		var msg cursorMessage
		if json.Unmarshal(readCursorBlob(ctx, db, ref), &msg) != nil || !strings.EqualFold(strings.TrimSpace(msg.Role), "assistant") {
			continue
		}
		for _, part := range cursorAssistantPartsFromContent(msg.Content) {
			if text, ok := part.(llmtypes.TextContent); ok {
				texts = append(texts, text.Text)
			}
		}
	}
	return texts
}
