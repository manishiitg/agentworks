package server

import "time"

const (
	pulseCheckDefaultGap    = 24 * time.Hour
	pulseTimingInstructions = "Pulse checks daily while on. Use a message wakeup for a useful follow-up; failures and messages use this same conversation. OFF starts no Pulse activity."
)
