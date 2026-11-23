package llmproviders

// validTokenUsageSources is the closed set of source labels the contract
// accepts for TokenUsageSource. Drift tests assert every coding-agent
// contract with SurfacesTokenUsage:true names one of these — guards
// against typos and free-form drift over time.
var validTokenUsageSources = map[string]struct{}{
	"msp":             {}, // Muse serve session/tokenUsage events
	"sdk-events":      {},
	"stream-json":     {}, // exact, parsed from the CLI's structured output
	"transcript-file": {}, // exact, parsed from a CLI-written transcript file
	"estimated":       {}, // approximate; adapter heuristically guesses
	"exec-json":       {}, // exact, parsed from the CLI's single-shot JSON result envelope
}

// IsValidTokenUsageSource reports whether s is one of the accepted
// TokenUsageSource labels.
func IsValidTokenUsageSource(s string) bool {
	_, ok := validTokenUsageSources[s]
	return ok
}
