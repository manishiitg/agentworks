package cursorcli

import (
	"os"

	"strings"
)

func buildCursorStructuredEnv(apiKey string) []string {
	env := os.Environ()
	if strings.TrimSpace(apiKey) != "" {
		env = append(env, "CURSOR_API_KEY="+strings.TrimSpace(apiKey))
	}
	return env
}
