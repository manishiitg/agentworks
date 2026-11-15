package server

import "fmt"

const serverOnlyInteractiveMessage = "This runs on the server. Add it to the Team to use it online."

// This is an execution/authoring rule, not a file-reading rule. Local source
// packages remain readable by the existing Team receive-online hand-off.
func requireServerInteractive() error {
	if IsLocalMode() {
		return fmt.Errorf("%s", serverOnlyInteractiveMessage)
	}
	return nil
}
