// Shared constants for event management
// This ensures consistent limits across frontend components

// Maximum events per session in memory.
// Lower = faster renders + less memory. "Load Older Events" handles history.
// Keep enough recent workflow activity for normal continuation while avoiding
// multi-session tabs retaining tens of MB of event objects.
export const MAX_EVENTS_TO_PROCESS = 1000

// Cleanup threshold: start cleanup above the retained max to avoid pruning on
// every small batch once a session reaches the cap.
export const CLEANUP_THRESHOLD = 1200
