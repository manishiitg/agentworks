package server

import (
	"fmt"
	"log"
)

const serverOnlyInteractiveMessage = "This runs only in a Crew on the server."

// This is an execution/authoring rule, not a file-reading rule.
func requireServerInteractive(operation string) error {
	if IsLocalMode() {
		log.Printf("[SERVER_ONLY_INTERACTIVE] operation=%s mode=local outcome=refused", operation)
		return fmt.Errorf("%s", serverOnlyInteractiveMessage)
	}
	return nil
}
