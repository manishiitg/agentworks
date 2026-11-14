package claudecode

import "encoding/json"

// modelUsage includes helper models too. Select only the native main model;
// don't pick the largest window or let a helper silently replace its capacity.
func (u *claudeLiveUsage) observeModelUsage(models map[string]json.RawMessage) bool {
	model := u.model
	if model == "" && len(models) == 1 {
		for name := range models {
			model = name
		}
	}
	raw, ok := models[model]
	if !ok {
		return false
	}
	var usage struct {
		Window int `json:"contextWindow"`
	}
	if json.Unmarshal(raw, &usage) != nil || usage.Window <= 0 {
		return false
	}
	u.model, u.window = model, usage.Window
	return true
}

func (u *claudeLiveUsage) observeModel(model string) {
	if model != "" && model != u.model {
		u.model = model
		u.window = 0
	}
}
