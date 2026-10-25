package virtualtools

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
)

func notificationRoutesSchema(fields, sections interface{}) map[string]interface{} {
	return map[string]interface{}{
		"type": "array", "maxItems": 20,
		"description": "Route-specific run outcomes in one notification. Use exact routing_step_id plus route_id from major routing steps, never branch choices. Include only routes whose run/review evidence you inspected; a route not reviewed is not clean. Keep shared work in the top-level message/sections. Do not repeat these route bodies in message_for_user: the backend adds them to the channel-neutral delivery text. When supplying a custom rich-channel body, render each intended fact exactly once. Prefer this over legacy summary_route even for one route.",
		"items": map[string]interface{}{
			"type": "object", "additionalProperties": false,
			"required": []string{"routing_step_id", "route_id", "title", "status", "message"},
			"properties": map[string]interface{}{
				"routing_step_id": map[string]interface{}{"type": "string", "minLength": 1, "maxLength": 200},
				"route_id":        map[string]interface{}{"type": "string", "minLength": 1, "maxLength": 200},
				"label":           map[string]interface{}{"type": "string", "maxLength": 200, "description": "Human-readable route name from the plan, not its identity."},
				"title":           map[string]interface{}{"type": "string", "minLength": 1, "maxLength": 150},
				"status":          map[string]interface{}{"type": "string", "enum": []string{"completed", "failed", "partial", "blocked", "waiting_for_user", "waiting_for_platform", "monitoring", "informational", "no_run"}},
				"message":         map[string]interface{}{"type": "string", "minLength": 1, "maxLength": 6000},
				"fields":          fields, "sections": sections,
			},
		},
	}
}

func notificationRoutesFromArg(raw interface{}) ([]services.NotificationRouteSummary, error) {
	if raw == nil {
		return nil, nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("summary_routes: %w", err)
	}
	var routes []services.NotificationRouteSummary
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&routes); err != nil {
		return nil, fmt.Errorf("summary_routes: %w", err)
	}
	if len(routes) > 20 {
		return nil, fmt.Errorf("summary_routes supports at most 20 routes per notification")
	}
	seen := map[[2]string]bool{}
	for i := range routes {
		r := &routes[i]
		r.RoutingStepID, r.RouteID = strings.TrimSpace(r.RoutingStepID), strings.TrimSpace(r.RouteID)
		r.Label, r.Title, r.Message = strings.TrimSpace(r.Label), strings.TrimSpace(r.Title), strings.TrimSpace(r.Message)
		key := [2]string{r.RoutingStepID, r.RouteID}
		if key[0] == "" || key[1] == "" || len(key[0]) > 200 || len(key[1]) > 200 || seen[key] {
			return nil, fmt.Errorf("summary_routes[%d] requires a unique, non-empty routing_step_id/route_id pair (maximum 200 bytes each)", i)
		}
		seen[key] = true
		status := normalizedNotificationSummaryStatus(r.Status)
		if status == "" || status != r.Status || r.Title == "" || r.Message == "" || len(r.Title) > 150 || len(r.Label) > 200 || len(r.Message) > 6000 || len(r.Fields) > 10 || len(r.Sections) > 12 {
			return nil, fmt.Errorf("summary_routes[%d] requires an explicit summary status, title and message within the advertised limits", i)
		}
	}
	return routes, nil
}

// Append route facts to the channel-neutral delivery text. A caller-supplied
// Gmail HTML body is already the complete email presentation and must remain
// untouched; appending this text as another HTML section duplicates the same
// facts below designed run-summary cards. When there is no custom HTML body,
// Gmail converts this completed channel-neutral text into one HTML body later.
func appendNotificationRouteContent(message string, routes []services.NotificationRouteSummary) string {
	for _, route := range routes {
		label := route.Label
		if label == "" {
			label = route.RouteID
		}
		heading := label + " — " + route.Title
		body := strings.ReplaceAll(route.Status, "_", " ") + "\n" + route.Message
		for _, field := range route.Fields {
			body += "\n" + field.Label + ": " + field.Value
		}
		for _, section := range route.Sections {
			body += "\n\n" + section.Heading + "\n" + section.Body
		}
		message += "\n\n" + heading + "\n" + body
	}
	return message
}
