package server

import "encoding/json"

func vaultPromoteJSON(value any) json.RawMessage {
	data, _ := json.Marshal(value)
	return data
}
