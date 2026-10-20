package virtualtools

import (
	"strings"
)

func stringArg(args map[string]interface{}, key string) string {
	value, _ := args[key].(string)
	return strings.TrimSpace(value)
}

func intArg(raw interface{}) int {
	switch value := raw.(type) {
	case int:
		return value
	case float64:
		return int(value)
	default:
		return 0
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
