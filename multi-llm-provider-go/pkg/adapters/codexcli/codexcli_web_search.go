package codexcli

import (
	"encoding/json"
	"strings"
)

// Codex's native web search, as both transports record it (checked live against
// codex-cli 0.160.1, 2026-10-07):
//
//	exec --json   item.started   {"type":"web_search","query":"","action":{"type":"other"}}
//	              item.completed {"type":"web_search","query":"…","action":{"type":"search","query":"…"},
//	                              "results":[{"title","url","domain","snippet",…}]}
//	rollout       event_msg item_completed {"type":"Extension","kind":"web.search",
//	              "query","action","results"} (older builds: {"type":"WebSearch","query","action"},
//	              no results)
//
// An opened page is a search whose action is {"type":"openPage"|"open_page","url"}
// and whose query is that URL; "find_in_page" carries a url and a pattern.
//
// The chat's search card (agent_go frontend webSearchToolCall.ts) reads the
// query from the call arguments or, when the arguments are not on the event it
// has, from a `Web search results for query: "…"` header in the result, and the
// result rows from a `Links: [{"title","url"}]` block. The end chunk's result
// uses that shape so the card shows the query and sources without a second
// event shape.

type codexWebSearchResult struct {
	Title   string `json:"title,omitempty"`
	URL     string `json:"url,omitempty"`
	Domain  string `json:"domain,omitempty"`
	Snippet string `json:"snippet,omitempty"`
}

// codexWebSearchArgs is the call arguments: the query plus Codex's own action
// object (search queries, or the opened page's url).
func codexWebSearchArgs(query string, action json.RawMessage) string {
	query = strings.TrimSpace(query)
	compactAction := compactCodexJSON(action)
	if query == "" && !codexWebSearchActionHasDetail(compactAction) {
		return ""
	}
	args := map[string]json.RawMessage{}
	if query != "" {
		q, _ := json.Marshal(query)
		args["query"] = q
	}
	if compactAction != "" {
		args["action"] = json.RawMessage(compactAction)
	}
	out, err := json.Marshal(args)
	if err != nil {
		return ""
	}
	return string(out)
}

// codexWebSearchActionHasDetail is false for the placeholder action Codex puts
// on item.started ({"type":"other"}), which says nothing about the search.
func codexWebSearchActionHasDetail(action string) bool {
	if action == "" {
		return false
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(action), &fields) != nil {
		return false
	}
	for key, value := range fields {
		if key == "type" {
			continue
		}
		if v := strings.TrimSpace(string(value)); v != "" && v != "null" && v != `""` && v != "[]" {
			return true
		}
	}
	return false
}

// codexWebSearchResultText renders a completed search as
//
//	Web search results for query: "<query>"
//
//	Links: [{"title":"…","url":"…"},…]
//
// Results without a URL (a failed page open reports only a title) are dropped
// from the links. With no query and no links it returns "".
func codexWebSearchResultText(query string, action json.RawMessage, results []codexWebSearchResult) string {
	query = strings.Join(strings.Fields(query), " ")
	if query == "" {
		var a struct {
			Query   string   `json:"query"`
			Queries []string `json:"queries"`
			URL     string   `json:"url"`
		}
		_ = json.Unmarshal(action, &a)
		switch {
		case strings.TrimSpace(a.Query) != "":
			query = strings.Join(strings.Fields(a.Query), " ")
		case len(a.Queries) > 0:
			query = strings.Join(strings.Fields(a.Queries[0]), " ")
		default:
			query = strings.TrimSpace(a.URL)
		}
	}
	type link struct {
		Title string `json:"title"`
		URL   string `json:"url"`
	}
	links := make([]link, 0, len(results))
	for _, r := range results {
		if url := strings.TrimSpace(r.URL); url != "" {
			links = append(links, link{Title: strings.TrimSpace(r.Title), URL: url})
		}
	}
	var b strings.Builder
	if query != "" {
		b.WriteString(`Web search results for query: "` + query + `"`)
	}
	if len(links) > 0 {
		encoded, err := json.Marshal(links)
		if err == nil {
			if b.Len() > 0 {
				b.WriteString("\n\n")
			}
			b.WriteString("Links: ")
			b.Write(encoded)
		}
	}
	return b.String()
}
