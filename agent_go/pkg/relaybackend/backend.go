// Package relaybackend selects the operator-configured Relay implementation.
package relaybackend

import (
	"fmt"
	"os"
	"reflect"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/relayapi"
)

func Name() (string, error) {
	name := strings.TrimSpace(os.Getenv("AGENTWORKS_RELAY_BACKEND"))
	if name == "" {
		name = "builtin"
	}
	if name != "builtin" && name != "private" {
		return "", fmt.Errorf("unknown Relay backend %q", name)
	}
	return name, nil
}

// Resolve never falls back after private selection. Selection is process
// configuration and is never taken from an HTTP, MCP or project argument.
func Resolve() (*relayapi.Factory, error) {
	name, err := Name()
	if err != nil || name == "builtin" {
		return nil, err
	}
	return validateFactory(privateFactory())
}

func validateFactory(f *relayapi.Factory) (*relayapi.Factory, error) {
	if f == nil {
		return nil, fmt.Errorf("private Relay backend is not compiled into this artifact")
	}
	if f.ID != "relays" || f.Version != relayapi.Version {
		return nil, fmt.Errorf("incompatible private Relay contract")
	}
	isNil := func(v any) bool {
		if v == nil {
			return true
		}
		switch reflect.ValueOf(v).Kind() {
		case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Func, reflect.Slice:
			return reflect.ValueOf(v).IsNil()
		}
		return false
	}
	if isNil(f.Files) || f.Run == nil || isNil(f.Releases) || f.SourceInspector == "" {
		return nil, fmt.Errorf("incomplete private Relay contract")
	}
	copy := *f
	return &copy, nil
}
