package main

import (
	"crypto/subtle"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
)

const workspaceAPITokenEnv = "WORKSPACE_API_TOKEN"

// requireWorkspaceAPIToken protects every /api route when AgentWorks
// launches the workspace service with a shared token. The token is deliberately
// not exposed to coding CLI environments, preventing a sandboxed CLI from
// asking this unsandboxed service to execute a broader command.
//
// Empty-token mode is retained for standalone workspace-server compatibility.
func requireWorkspaceAPIToken() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !workspaceTokenAuthorized(c) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "workspace execution authorization required"})
			return
		}
		c.Next()
	}
}

// requireWorkspaceAPITokenOnAPI applies the same check to every /api request
// before any global middleware runs. The remote router forwards and aborts
// before route groups execute, so the group-level check alone would be
// skipped for forwarded requests and /api/remote/*.
func requireWorkspaceAPITokenOnAPI() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !strings.HasPrefix(c.Request.URL.Path, "/api/") || c.Request.Method == http.MethodOptions {
			c.Next()
			return
		}
		if !workspaceTokenAuthorized(c) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "workspace execution authorization required"})
			return
		}
		c.Next()
	}
}

// workspaceTokenAuthorized reports whether a request carries the shared
// workspace token (or a per-user token already verified in server mode).
// Empty-token mode is retained for standalone workspace-server compatibility.
func workspaceTokenAuthorized(c *gin.Context) bool {
	if c.GetBool(serverUserAuthenticatedFlag) {
		return true
	}
	expected := strings.TrimSpace(os.Getenv(workspaceAPITokenEnv))
	if expected == "" {
		return true
	}
	actual := strings.TrimSpace(c.GetHeader("X-Workspace-Token"))
	if actual == "" {
		if authorization := strings.TrimSpace(c.GetHeader("Authorization")); strings.HasPrefix(authorization, "Bearer ") {
			actual = strings.TrimSpace(strings.TrimPrefix(authorization, "Bearer "))
		}
	}
	return subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) == 1
}

// New managed-file operations never inherit legacy unauthenticated mode.
func requireConfiguredWorkspaceAPIToken() gin.HandlerFunc {
	authenticate := requireWorkspaceAPIToken()
	return func(c *gin.Context) {
		if strings.TrimSpace(os.Getenv(workspaceAPITokenEnv)) == "" {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "Configure WORKSPACE_API_TOKEN on both AgentWorks and the workspace service to enable external file operations"})
			return
		}
		authenticate(c)
	}
}
