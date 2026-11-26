package cursorcli

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// AdoptNativeInput pins the accepted user blob without replaying its text.
func AdoptNativeInput(owner, message string, acceptedAt time.Time) error {
	session, ok := cursorPersistentRegistry.Get(strings.TrimSpace(owner))
	if !ok || session == nil {
		return fmt.Errorf("no Cursor session for %q", owner)
	}
	session.retainedMu.Lock()
	defer session.retainedMu.Unlock()
	path := session.resolveRetainedStoreLocked()
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return err
	}
	defer db.Close()
	refs, err := cursorStoreLatestRootRefs(context.Background(), db)
	if err != nil {
		return err
	}
	matched := -1
	for index, ref := range refs {
		var row cursorMessage
		if json.Unmarshal(readCursorBlob(context.Background(), db, ref), &row) == nil && row.Role == "user" &&
			strings.Join(strings.Fields(cursorUserQueryFromContent(row.Content)), " ") == strings.Join(strings.Fields(message), " ") {
			matched = index
		}
	}
	if matched < 0 {
		return fmt.Errorf("accepted Cursor prompt not found")
	}
	baseline := make(map[string]struct{}, matched)
	for _, ref := range refs[:matched] {
		baseline[ref] = struct{}{}
	}
	session.retainedInput = &cursorRetainedInput{storeDB: path, query: strings.Join(strings.Fields(message), " "), message: message, sentAt: acceptedAt, baseline: baseline}
	primeCursorRetainedProgress(owner, session.retainedInput)
	return nil
}
