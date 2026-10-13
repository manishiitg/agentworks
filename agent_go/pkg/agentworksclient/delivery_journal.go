package agentworksclient

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// An offer is written before the adapter emits it. This deliberately chooses
// at-most-once automatic offers over duplicate turns after an ambiguous crash.
// Offers are not admission receipts; the agent must still acknowledge them.
type deliveryJournal struct {
	SeenThrough int `json:"seen_through,omitempty"`
	path        string
	Messages    map[string]AgentLiveMessage `json:"messages"`
	Retry       map[string]bool             `json:"retry,omitempty"`
}

func privateLiveDir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || CheckPrivateAccess(path, info) != nil {
		return errors.New("live state must be a private directory, not a symlink")
	}
	return nil
}

func atomicLiveJSON(path string, value any) error {
	if info, err := os.Lstat(path); err == nil && (!info.Mode().IsRegular() || CheckPrivateAccess(path, info) != nil) {
		return errors.New("refusing non-private or redirected live state")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(b) > 32<<20 {
		return errors.New("live journal exceeds 32 MiB; explicitly read and acknowledge pending messages")
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".live-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = SecurePrivateFile(f); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = syncJournalFile(f)
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return syncJournalFile(dir)
}

func readLiveJSON(path string, value any) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || CheckPrivateAccess(path, info) != nil || info.Size() > 32<<20 {
		return errors.New("invalid private live state")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, value)
}

func liveHash(parts ...string) string {
	b, _ := json.Marshal(parts)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:16])
}

func newDeliveryJournal(root string, client *Client, attachment AgentAttachment) (*deliveryJournal, error) {
	if root == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			return nil, err
		}
		root = filepath.Join(cache, "agentworks", "channel-offers")
	}
	if err := privateLiveDir(root); err != nil {
		return nil, err
	}
	j := &deliveryJournal{path: filepath.Join(root, liveHash(client.baseURL, attachment.AgentID, attachment.SessionID)+".json"), Messages: map[string]AgentLiveMessage{}}
	if err := readLiveJSON(j.path, j); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read channel journal: %w", err)
	}
	if j.Messages == nil {
		j.Messages = map[string]AgentLiveMessage{}
	}
	for _, m := range j.Messages {
		if m.Sequence > j.SeenThrough {
			j.SeenThrough = m.Sequence
		}
		m.Message = ""
		j.Messages[m.MessageID] = m
	}
	return j, nil
}

func (j *deliveryJournal) offer(m AgentLiveMessage) (bool, error) {
	if _, ok := j.Messages[m.MessageID]; (ok || m.Sequence <= j.SeenThrough) && !j.Retry[m.MessageID] {
		return false, nil
	}
	if len(j.Messages) >= 1000 {
		for id, v := range j.Messages {
			if v.Sequence <= j.SeenThrough-500 && !j.Retry[id] {
				delete(j.Messages, id)
			}
		}
	}
	previous := j.SeenThrough
	old, existed := j.Messages[m.MessageID]
	retry := j.Retry[m.MessageID]
	j.SeenThrough = max(j.SeenThrough, m.Sequence)
	delete(j.Retry, m.MessageID)
	m.Message = ""
	j.Messages[m.MessageID] = m
	if err := atomicLiveJSON(j.path, j); err != nil {
		delete(j.Messages, m.MessageID)
		if existed {
			j.Messages[m.MessageID] = old
		}
		if retry {
			j.Retry[m.MessageID] = true
		}
		j.SeenThrough = previous
		return false, err
	}
	return true, nil
}

func (j *deliveryJournal) forget(id string) error {
	if _, exists := j.Messages[id]; !exists {
		return nil
	}
	if j.Retry == nil {
		j.Retry = map[string]bool{}
	}
	previous := j.Retry[id]
	// Only a known failed notification may retry. Keep the high-water mark
	// monotonic and retain this explicit exception across a restart.
	j.Retry[id] = true
	if err := atomicLiveJSON(j.path, j); err != nil {
		if previous {
			j.Retry[id] = true
		} else {
			delete(j.Retry, id)
		}
		return err
	}
	return nil
}
