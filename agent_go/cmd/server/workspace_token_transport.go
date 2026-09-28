package server

import "github.com/manishiitg/coding-agent-loop/agent_go/pkg/wsauth"

// Every in-process call to the workspace API carries the workspace token:
// the service requires it on all /api routes when WORKSPACE_API_TOKEN is set.
func init() { wsauth.InstallDefault() }
