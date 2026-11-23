package cursorcli

import (
	"os"
	"testing"

	"github.com/joho/godotenv"
)

// TestMain seeds only the Vertex/Gemini judge credentials from the repo-root
// .env so the final-extraction judge e2e can run without the caller exporting
// them by hand. We deliberately load just these keys (not the whole .env) to
// avoid polluting the process environment with unrelated provider keys, which
// would trip env-isolation assertions in other tests. Existing process env
// always wins (we never override an already-set value), so an explicit
// `GEMINI_API_KEY=... go test` still takes precedence.
//
// cursorcli was one of the interactive adapters missing this seeding, so its
// semantic-judge e2e tests failed on a missing key rather than a real
// contract violation.
func TestMain(m *testing.M) {
	// Cursor tests must never inherit an expensive model from a developer's
	// current Cursor UI setting. Legacy test constructors use "cursor-cli" as
	// the model selector; the adapter maps that selector to Composer 2.5 under this
	// explicit test-only flag. Individual mapping tests may temporarily unset it.
	_ = os.Setenv(EnvCursorCLITestModel, "composer-2.5")
	judgeKeys := []string{"GEMINI_API_KEY", "VERTEX_API_KEY", "GOOGLE_API_KEY"}
	for _, p := range []string{".env", "../../../.env", "../../../../.env"} {
		vals, err := godotenv.Read(p)
		if err != nil {
			continue
		}
		for _, k := range judgeKeys {
			if v, ok := vals[k]; ok && os.Getenv(k) == "" {
				_ = os.Setenv(k, v)
			}
		}
		break
	}
	os.Exit(m.Run())
}
