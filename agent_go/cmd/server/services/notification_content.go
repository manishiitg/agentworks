package services

// NotificationContent is an optional, typed, per-channel rendering of a single
// notification. It rides alongside the plain `message` string on a
// NotificationDestination so connectors can opt into richer output without the
// shared SendNotification signature changing.
//
// Model: `Text` (and the plain `message` arg) is the common lowest-common-
// denominator every channel can render. A channel with a non-nil slice here
// renders that instead; a channel without one falls back to the plain message.
// Add Slack/WhatsApp slices the same way when those channels grow rich content.
type NotificationContent struct {
	Text    string               // top-level summary text; delivery may append the typed route entries
	Summary *NotificationSummary // channel-neutral structured summary for durable/internal surfaces
	Gmail   *GmailContent        // Gmail-specific rendering (nil = derive from message)
}

// NotificationSummary is the channel-neutral representation of a structured
// workflow notification. External connectors may render it, while the
// org_dashboard connector persists it without parsing Slack blocks or Gmail
// HTML. Kind is one of general, run_summary, or pulse_summary. Only the latter
// two are durable Org Dashboard inputs. Status describes what the workflow is
// doing now (for example completed, blocked, or waiting_for_user). The title,
// message, facts, and sections provide the explanation; there is no separate
// owner, state, or next-action bookkeeping.
type NotificationSummary struct {
	Kind     string                       `json:"kind"`
	Title    string                       `json:"title,omitempty"`
	Status   string                       `json:"status,omitempty"`
	Route    string                       `json:"route,omitempty"`
	Fields   []NotificationSummaryField   `json:"fields,omitempty"`
	Sections []NotificationSummarySection `json:"sections,omitempty"`
	Routes   []NotificationRouteSummary   `json:"routes,omitempty"`
}

// NotificationRouteSummary describes only the evidence for one major route.
// The pair is the identity: route IDs alone need not be unique across routers.
// Branch choices are not sub-workflows and do not get separate route summaries.
type NotificationRouteSummary struct {
	RoutingStepID string                       `json:"routing_step_id"`
	RouteID       string                       `json:"route_id"`
	Label         string                       `json:"label,omitempty"`
	Title         string                       `json:"title"`
	Status        string                       `json:"status"`
	Message       string                       `json:"message"`
	Fields        []NotificationSummaryField   `json:"fields,omitempty"`
	Sections      []NotificationSummarySection `json:"sections,omitempty"`
}

type NotificationSummaryField struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

type NotificationSummarySection struct {
	Heading string `json:"heading"`
	Body    string `json:"body"`
}

// GmailContent is the Gmail-specific rendering. Every field is optional and
// falls back: Subject → derived from the message's first line; HTMLBody → none;
// CC → none; Attachments → none. When HTMLBody is absent, the shared message is
// escaped and sent as the single HTML body.
type GmailContent struct {
	Subject     string
	CC          []string
	HTMLBody    string   // optional complete rich HTML body
	Attachments []string // absolute file paths on the server host (see Gmail raw send)
}

// gmailContentFrom returns the Gmail content slice from a destination, or nil.
func gmailContentFrom(dest *NotificationDestination) *GmailContent {
	if dest != nil && dest.Content != nil {
		return dest.Content.Gmail
	}
	return nil
}
