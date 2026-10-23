package server

import (
	"fmt"
	"strings"
)

func normalizePulseOutwardActions(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "ask":
		return "ask", nil
	case "allow":
		return "allow", nil
	case "decide":
		return "decide", nil
	}
	return "", fmt.Errorf("Autopilot autonomy must be ask, allow or decide")
}
