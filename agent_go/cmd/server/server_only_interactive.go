package server

import (
	"fmt"
	"github.com/google/uuid"
	"log"
)

const serverOnlyInteractiveMessage = "This runs on the server. Add it to the Team to use it online."

// This is an execution/authoring rule, not a file-reading rule. Local source
// packages remain readable by the existing Team receive-online hand-off.
func requireServerInteractive() error {
	if IsLocalMode() {
		log.Printf("[SERVER_ONLY_INTERACTIVE] operation=%s mode=local outcome=refused", uuid.NewString())
		return fmt.Errorf("%s", serverOnlyInteractiveMessage)
	}
	return nil
}
