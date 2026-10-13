package main

import "github.com/manishiitg/coding-agent-loop/mcp-gateway/pkg/vaultruntime"

// Public builds never import private source. Private composition replaces only
// this factory file, retaining host selection and explicit operator opt-in.
func privateVaultRuntime() *vaultruntime.Factory { return nil }
