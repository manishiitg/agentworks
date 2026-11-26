package oauth

import "errors"

var (
	// Configuration errors
	ErrMissingAuthURL  = errors.New("oauth: missing authorization URL (auth_url)")
	ErrMissingTokenURL = errors.New("oauth: missing token URL (token_url)")
	ErrMissingClientID = errors.New("oauth: missing client ID")

	// Token errors
	ErrNoValidToken      = errors.New("oauth: no valid token available - run authentication flow")
	ErrTokenExpired      = errors.New("oauth: token expired and refresh failed")
	ErrTokenFileNotFound = errors.New("oauth: token file not found")
	ErrInvalidTokenFile  = errors.New("oauth: token file is corrupted or invalid")

	// Flow errors
	ErrCallbackTimeout = errors.New("oauth: timeout waiting for authorization callback")
	ErrInvalidState    = errors.New("oauth: invalid state parameter in callback")
	ErrAuthDenied      = errors.New("oauth: user denied authorization")
	ErrCodeExchange    = errors.New("oauth: failed to exchange authorization code for token")

	// Discovery errors
	ErrDiscoveryFailed      = errors.New("oauth: failed to discover endpoints from 401 response")
	ErrNoAuthHeader         = errors.New("oauth: no WWW-Authenticate header in 401 response")
	ErrInvalidAuthHeader    = errors.New("oauth: invalid WWW-Authenticate header format")
	ErrEndpointsNotFound    = errors.New("oauth: could not extract OAuth endpoints from response")
)
