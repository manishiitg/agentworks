package server

import (
	"net/http"
	"regexp"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/wsalias"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/wsauth"
)

// workspaceHTTPClient is a shared, connection-pooling HTTP client for workspace API calls.
// Reusing a single client avoids per-request TCP handshake overhead and enables keep-alive.
var workspaceHTTPClient = &http.Client{
	Timeout: 30 * time.Second,
	// wsalias: an old spelling of a moved Crew's path is translated before the request leaves (PLAT-442 step 4).
	Transport: wsalias.Transport(wsauth.Transport(&http.Transport{
		MaxIdleConns:        50,
		MaxIdleConnsPerHost: 50,
		IdleConnTimeout:     90 * time.Second,
	})),
}

// iterationRe matches "iteration-<number>" in folder names. Compiled once to avoid
// repeated compilation inside sort comparators.
var iterationRe = regexp.MustCompile(`iteration-(\d+)`)
