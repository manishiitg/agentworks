package agentworksclient

import (
	"encoding/json"
	"log"
)

// #2980: several individually bounded server inventories still need one shared
// tool budget. Remove complete rows, never bytes from an exact address.
func boundBridgeDirectoryReply(reply *LocalBridgeReply) {
	pages := []*json.RawMessage{}
	if len(reply.Result) > 0 {
		pages = append(pages, &reply.Result)
	}
	for i := range reply.Directories {
		pages = append(pages, &reply.Directories[i].Agents)
	}
	count := func() int {
		n := 0
		for _, page := range pages {
			var data struct {
				Agents []json.RawMessage `json:"agents"`
			}
			_ = json.Unmarshal(*page, &data)
			n += len(data.Agents)
		}
		return n
	}
	for {
		raw, err := json.Marshal(reply)
		if err == nil && len(raw) < 20_000 && count() <= 20 {
			if reply.Warning != "" {
				log.Printf("[AGENTBRIDGE] event=directory_bounded attachment=%s rows=%d bytes=%d", reply.Attachment.ID, count(), len(raw))
			}
			return
		}
		reply.Warning = "List shortened. Use query=<name or Team> and select network to find the rest."
		removed := false
		for i := len(pages) - 1; i >= 0; i-- {
			var page map[string]json.RawMessage
			if json.Unmarshal(*pages[i], &page) != nil {
				continue
			}
			var rows []json.RawMessage
			_ = json.Unmarshal(page["agents"], &rows)
			if len(rows) == 0 {
				continue
			}
			rows = rows[:len(rows)-1]
			page["agents"], _ = json.Marshal(rows)
			page["returned"], _ = json.Marshal(len(rows))
			page["truncated"] = json.RawMessage("true")
			*pages[i], _ = json.Marshal(page)
			removed = true
			break
		}
		if !removed {
			// Even summaries can overflow with many joined servers. Give a bounded
			// next step instead of corrupting JSON or any address.
			*reply = LocalBridgeReply{Error: "The joined directories exceed the listing limit. Select network and query=<name or Team>."}
			return
		}
	}
}
