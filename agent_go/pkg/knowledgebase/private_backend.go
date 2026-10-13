package knowledgebase

import "github.com/manishiitg/coding-agent-loop/agent_go/pkg/brainapi"

// Public builds never import private source. The private composition builder replaces
// this single file at compile time; selection still requires an explicit operator opt-in.
func privateBackendFactory() *brainapi.Factory { return nil }
