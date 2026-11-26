package mcpcache

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLegacyResourceAndPromptCacheStillLoadsTools(t *testing.T) {
	var entry CacheEntry
	if err := json.Unmarshal([]byte(`{"server_name":"demo","tools":[{"type":"function","function":{"name":"lookup"}}],"prompts":[{"name":"guide"}],"resources":[{"uri":"legacy://data","name":"Legacy"}]}`), &entry); err != nil {
		t.Fatal(err)
	}
	if len(entry.Tools) != 1 || entry.Tools[0].Function.Name != "lookup" {
		t.Fatal("legacy cache lost tools")
	}
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"prompts"`) || strings.Contains(string(data), `"resources"`) || strings.Contains(string(data), "legacy://data") {
		t.Fatal("retired resources persisted again")
	}
}
