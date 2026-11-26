package modelspecs

import (
	"slices"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// For is id's entry as provider serves it; false when the table has no entry.
func For(id, provider string) (*llmtypes.ModelMetadata, bool) {
	spec, ok := specs[id]
	if !ok {
		return nil, false
	}
	spec.ModelID, spec.Provider = id, provider
	spec.ReasoningEffortLevels = slices.Clone(spec.ReasoningEffortLevels)
	return &spec, true
}

// Has reports whether the table has an entry for id.
func Has(id string) bool {
	_, ok := specs[id]
	return ok
}
