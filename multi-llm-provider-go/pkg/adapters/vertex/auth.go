package vertex

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/interfaces"

	"cloud.google.com/go/auth/credentials"
)

// TokenCache manages OAuth token caching with expiration
type TokenCache struct {
	token     string
	expiresAt time.Time
	mu        sync.RWMutex
}

var globalTokenCache = &TokenCache{}

// GetAccessToken retrieves an access token using multiple authentication methods.
// It tries gcloud authentication first, then Application Default Credentials.
func GetAccessToken(ctx context.Context, logger interfaces.Logger) (string, error) {
	// Check cache first
	globalTokenCache.mu.RLock()
	if globalTokenCache.token != "" && time.Now().Before(globalTokenCache.expiresAt) {
		token := globalTokenCache.token
		globalTokenCache.mu.RUnlock()
		if logger != nil {
			logger.Debugf("Using cached access token (expires at %v)", globalTokenCache.expiresAt)
		}
		return token, nil
	}
	globalTokenCache.mu.RUnlock()

	// Try authentication methods in order
	var token string
	var err error

	// Method 1: Try gcloud auth
	token, err = getGCloudToken(ctx, logger)
	if err == nil && token != "" {
		// Cache the token (gcloud tokens typically expire in 1 hour)
		globalTokenCache.mu.Lock()
		globalTokenCache.token = token
		globalTokenCache.expiresAt = time.Now().Add(55 * time.Minute) // Cache for 55 minutes
		globalTokenCache.mu.Unlock()
		if logger != nil {
			logger.Infof("✅ Authenticated using gcloud auth")
		}
		return token, nil
	}
	if logger != nil {
		logger.Debugf("gcloud auth failed: %v", err)
	}

	// Method 2: Try Application Default Credentials
	token, err = getADCToken(ctx, logger)
	if err == nil && token != "" {
		// Cache the token
		globalTokenCache.mu.Lock()
		globalTokenCache.token = token
		globalTokenCache.expiresAt = time.Now().Add(55 * time.Minute)
		globalTokenCache.mu.Unlock()
		if logger != nil {
			logger.Infof("✅ Authenticated using Application Default Credentials")
		}
		return token, nil
	}
	if logger != nil {
		logger.Debugf("ADC auth failed: %v", err)
	}

	return "", fmt.Errorf("all authentication methods failed. Last error: %w", err)
}

// getGCloudToken retrieves an access token using gcloud CLI
func getGCloudToken(ctx context.Context, logger interfaces.Logger) (string, error) {
	if logger != nil {
		logger.Debugf("Attempting gcloud authentication...")
	}

	// Check if gcloud is available
	cmd := exec.CommandContext(ctx, "gcloud", "auth", "print-access-token")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("gcloud auth failed: %w", err)
	}

	token := strings.TrimSpace(string(output))
	if token == "" {
		return "", fmt.Errorf("gcloud returned empty token")
	}

	return token, nil
}

// getADCToken retrieves an access token using Application Default Credentials
func getADCToken(ctx context.Context, logger interfaces.Logger) (string, error) {
	if logger != nil {
		logger.Debugf("Attempting Application Default Credentials authentication...")
	}

	// Use Google Cloud auth library to get default credentials
	creds, err := credentials.DetectDefault(&credentials.DetectOptions{
		Scopes: []string{"https://www.googleapis.com/auth/cloud-platform"},
	})
	if err != nil {
		return "", fmt.Errorf("failed to detect default credentials: %w", err)
	}

	// Get token
	token, err := creds.Token(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get token from ADC: %w", err)
	}

	return token.Value, nil
}

// ClearTokenCache clears the cached token (useful for testing or forced refresh)
func ClearTokenCache() {
	globalTokenCache.mu.Lock()
	defer globalTokenCache.mu.Unlock()
	globalTokenCache.token = ""
	globalTokenCache.expiresAt = time.Time{}
}
