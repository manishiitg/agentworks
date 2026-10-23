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
	}
	return "", fmt.Errorf("Autopilot outward actions must be ask or allow")
}
