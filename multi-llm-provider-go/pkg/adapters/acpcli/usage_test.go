package acpcli

import (
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// A real agy turn reported fresh input apart from cache (total = input +
// output + cache). Treating it as cache-inclusive billed fresh input as $0.
func TestAgyFreshInputIsNotTreatedAsIncludingCache(t *testing.T) {
	cached := 275631
	fresh := &llmtypes.Usage{InputTokens: 101470, OutputTokens: 2988, TotalTokens: 380089, CacheTokens: &cached}
	if inclusive, known := promptIncludesCache(fresh); !known || inclusive {
		t.Fatalf("agy usage: inclusive=%v known=%v, want fresh input apart from cache", inclusive, known)
	}
	inclusiveUsage := &llmtypes.Usage{InputTokens: 377101, OutputTokens: 2988, TotalTokens: 380089, CacheTokens: &cached}
	if inclusive, known := promptIncludesCache(inclusiveUsage); !known || !inclusive {
		t.Fatalf("cache-inclusive usage: inclusive=%v known=%v", inclusive, known)
	}
}
