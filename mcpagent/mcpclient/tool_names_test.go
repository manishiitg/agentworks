package mcpclient

import (
	"regexp"
	"strings"
	"testing"
)

var providerToolName = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

// A Vault tool's real name carries its connection ID, which filled the 64
// characters: the tool word was cut off and a hash took its place, and agents
// could not read or retype the name (PLAT-856).
func TestToolNamesAreShortAndKeepTheToolWord(t *testing.T) {
	real := "notion_c-94acea49e7a07fdeae13b56d7e2ab43d__notion-fetch"
	if got := ToolNames("notion", []string{real}, true)[real]; got != "notion__notion-fetch" {
		t.Fatalf("visible name = %q", got)
	}
	// Invalid characters are cleaned, with nothing added.
	if got := ToolNames("neon", []string{"search page.v2"}, false)["search page.v2"]; got != "neon__search_page_v2" {
		t.Fatalf("cleaned name = %q", got)
	}
	// Over the limit the alias gives way, not the tool word.
	word := strings.Repeat("w", 50)
	if got := ToolNames("a_very_long_application", []string{word}, false)[word]; got != "a_very_long__"+word {
		t.Fatalf("long name = %q", got)
	}
	// A word that cannot fit keeps both ends, so similar tools stay apart.
	long := strings.Repeat("very_long_tool_name_", 5)
	names := ToolNames("neon_46fb", []string{long + "alpha", long + "omega"}, false)
	if names[long+"alpha"] == names[long+"omega"] {
		t.Fatalf("long tools collapsed to %q", names[long+"alpha"])
	}
	// Two real tools that clean to one name both get a tail from the real name alone.
	names = ToolNames("app", []string{"a.b", "a b", "other"}, false)
	if names["a.b"] == names["a b"] || !strings.HasPrefix(names["a.b"], "app__a_b_") || names["other"] != "app__other" {
		t.Fatalf("clash names = %v", names)
	}
	for _, name := range names {
		if !providerToolName.MatchString(name) {
			t.Fatalf("invalid name %q", name)
		}
	}
	if again := ToolNames("app", []string{"other", "a b", "a.b"}, false); again["a.b"] != names["a.b"] {
		t.Fatal("a name depended on list order")
	}
}

// Agents mix the spellings they see: notion_c_94acea...__notion-fetch matched
// neither the exact name nor the fully sanitized one, and the call failed
// (PLAT-856). Tools that really differ must not be guessed between.
func TestMatchToolNameAcceptsEverySpellingAndRefusesToGuess(t *testing.T) {
	real := "notion_c-94acea49__notion-fetch"
	tools := []string{real, "notion_c-94acea49__notion-get-users"}
	for _, asked := range []string{
		real, "notion__notion-fetch", "notion__notion_fetch", "NOTION__NOTION-FETCH", "notion-fetch",
		"notion_c_94acea49__notion-fetch", "notion_c-94acea49__notion_fetch",
	} {
		if got, options := MatchToolName(asked, "notion", tools, true); got != real || options != nil {
			t.Errorf("MatchToolName(%q) = %q %v", asked, got, options)
		}
	}
	if got, options := MatchToolName("notion__missing", "notion", tools, true); got != "" || options != nil {
		t.Fatalf("unknown tool matched: %q %v", got, options)
	}

	pair := []string{"app_c-1__get-item", "app_c-1__get_item"}
	if got, options := MatchToolName("app__GET_ITEM", "app", pair, true); got != "" || len(options) != 2 {
		t.Fatalf("different tools were guessed between: %q %v", got, options)
	}
	if got, _ := MatchToolName("app_c-1__get-item", "app", pair, true); got != "app_c-1__get-item" {
		t.Fatalf("an exact name must still win: %q", got)
	}
}
