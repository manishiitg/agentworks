package musecli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func AdoptNativeInput(owner, message string) error {
	key, err := musePersistentKey(owner)
	if err != nil {
		return err
	}
	musePersistentPool.Lock()
	defer musePersistentPool.Unlock()
	entry := musePersistentPool.m[key]
	if entry == nil {
		return fmt.Errorf("no Muse session for %q", owner)
	}
	path := entry.logPath
	if path == "" {
		path = museSessionLogPath(entry.nativeSessionID, entry.accountDataHome)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var baseline int64 = -1
	for _, line := range strings.Split(string(raw), "\n") {
		var row museIntakeRecord
		if json.Unmarshal([]byte(line), &row) != nil || row.PayloadType != "runtime.user_intent.accepted" {
			continue
		}
		var text strings.Builder
		for _, block := range row.Payload.RefillBlocks {
			if block.Kind == "text" {
				text.WriteString(block.Text)
			}
		}
		if strings.TrimSpace(text.String()) == strings.TrimSpace(message) {
			baseline = row.Sequence
		}
	}
	if baseline < 0 {
		return fmt.Errorf("accepted Muse prompt not found")
	}
	entry.retainedBaselineSequence = baseline
	entry.lastSubmittedPrompt = museTerminalPrompt(message)
	return nil
}
