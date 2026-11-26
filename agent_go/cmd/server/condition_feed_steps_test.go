package server

import (
	"fmt"
	"testing"
)

// An item someone is working on must not drop off its row when 20 newer items arrive (#3625).
func TestConditionFeedTrimKeepsItemsBeingWorkedOn(t *testing.T) {
	steps := []string{"New", "Fixing", "Done"}
	items := []conditionFeedItem{{ID: "working", At: 1, Step: "Fixing"}, {ID: "linked", At: 2, Tasks: []conditionFeedTask{{Inbox: "i", Message: "m"}}}, {ID: "finished", At: 3, Step: "Done"}}
	for i := 0; i < 30; i++ {
		items = append(items, conditionFeedItem{ID: fmt.Sprint("new-", i), At: int64(100 + i)})
	}
	kept := map[string]bool{}
	for _, item := range conditionFeedTrim(items, steps) {
		kept[item.ID] = true
	}
	if !kept["working"] || !kept["linked"] || kept["finished"] || len(kept) != 22 {
		t.Fatalf("kept %d items, working=%v linked=%v finished=%v", len(kept), kept["working"], kept["linked"], kept["finished"])
	}
}
