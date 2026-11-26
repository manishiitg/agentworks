package codexcli

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
func TestMain(m *testing.M) {
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
