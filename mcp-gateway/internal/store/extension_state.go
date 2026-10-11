package store

import (
	"encoding/json"
	"errors"
	"strings"
)

// ExtensionState is opaque, encrypted host configuration. Product algorithms
// and model/provider implementations remain in the separately built product.
type ExtensionState struct {
	Revision int
	Data     json.RawMessage
}

var ErrExtensionStateConflict = errors.New("extension state revision conflict")

func (s *MemoryStore) ExtensionState(product, workspace, key string) ExtensionState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	row := s.extensionState[product+"\x00"+workspace+"\x00"+key]
	row.Data = append(json.RawMessage(nil), row.Data...)
	return row
}
func (s *MemoryStore) CompareAndSwapExtensionState(product, workspace, key string, data json.RawMessage, expected int) (ExtensionState, error) {
	if product == "" || workspace == "" || key == "" || strings.ContainsAny(product+workspace+key, "\x00") || len(data) > 65536 || !json.Valid(data) || expected < 0 {
		return ExtensionState{}, errors.New("invalid extension state")
	}
	var row ExtensionState
	var conflict bool
	func() {
		s.mu.Lock()
		defer s.persistUnlock()
		id := product + "\x00" + workspace + "\x00" + key
		if s.extensionState[id].Revision != expected {
			conflict = true
			return
		}
		if s.extensionState == nil {
			s.extensionState = map[string]ExtensionState{}
		}
		row = ExtensionState{Revision: expected + 1, Data: append(json.RawMessage(nil), data...)}
		s.extensionState[id] = row
	}()
	if conflict {
		return ExtensionState{}, ErrExtensionStateConflict
	}
	if err := s.PersistenceError(); err != nil {
		return ExtensionState{}, err
	}
	return row, nil
}
