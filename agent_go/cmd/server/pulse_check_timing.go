package server

import "time"

const (
	pulseCheckDefaultGap    = 24 * time.Hour
	pulseTimingInstructions = "Autopilot checks daily while on. Use a message wakeup for a useful follow-up; failures and messages use this same conversation. OFF starts no Autopilot activity."
)
