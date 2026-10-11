package producthost

import (
	"errors"
	"os"
	"strings"
)

// ServiceTokenFromEnv never falls back to the Vault admin credential. Both
// services receive the per-product secret through protected environment/files.
func ServiceTokenFromEnv(product string) (string, error) {
	if !validProductID.MatchString(product) {
		return "", errors.New("invalid product ID")
	}
	key := "AGENTWORKS_PRODUCT_" + strings.ToUpper(strings.ReplaceAll(product, "-", "_")) + "_SERVICE_TOKEN"
	token := strings.TrimSpace(os.Getenv(key))
	if file := strings.TrimSpace(os.Getenv(key + "_FILE")); file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return "", errors.New("product credential unavailable")
		}
		token = strings.TrimSpace(string(data))
	}
	if len(token) < 32 {
		return "", errors.New("dedicated product credential required")
	}
	return token, nil
}
