package services

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/livefeed"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	_ "modernc.org/sqlite"
)

const orgDashboardHistoryLimitPerKind = 50

// OrgDashboardNotification is the durable, channel-neutral record rendered by
// the Organization page. It is written by workflow summary delivery through the
// org_dashboard connector, never by parsing an email or Slack payload.
type OrgDashboardNotification struct {
	ID            string                       `json:"id"`
	WorkspacePath string                       `json:"workspace_path"`
	Kind          string                       `json:"kind"`
	Title         string                       `json:"title,omitempty"`
	Status        string                       `json:"status"`
	Route         string                       `json:"route,omitempty"`
	Message       string                       `json:"message"`
	Fields        []NotificationSummaryField   `json:"fields,omitempty"`
	Sections      []NotificationSummarySection `json:"sections,omitempty"`
	Routes        []NotificationRouteSummary   `json:"routes,omitempty"`
	CreatedAt     string                       `json:"created_at"`
}

type OrgDashboardWorkflowNotifications struct {
	WorkspacePath string                           `json:"workspace_path"`
	RunSummary    *OrgDashboardNotification        `json:"run_summary,omitempty"`
	Recent        []OrgDashboardNotification       `json:"recent,omitempty"`
	ByRoute       []OrgDashboardRouteNotifications `json:"by_route,omitempty"`
	Error         string                           `json:"error,omitempty"`
}

type OrgDashboardRouteNotifications struct {
	RoutingStepID string                    `json:"routing_step_id,omitempty"`
	RouteID       string                    `json:"route_id"`
	Label         string                    `json:"label"`
	Legacy        bool                      `json:"legacy,omitempty"`
	RunSummary    *OrgDashboardNotification `json:"run_summary,omitempty"`
}

// OrgDashboardConnector is an always-on internal notification connector. It
// persists run_summary calls even when every external
// channel is disabled or fails.
type OrgDashboardConnector struct {
	mu sync.Mutex
}

func NewOrgDashboardConnector() *OrgDashboardConnector { return &OrgDashboardConnector{} }

func (c *OrgDashboardConnector) Name() string    { return "org_dashboard" }
func (c *OrgDashboardConnector) IsEnabled() bool { return true }

// Blocking feedback requests are not Org Dashboard events.
func (c *OrgDashboardConnector) SendNotification(context.Context, string, string, string, *ButtonOptions, *NotificationDestination) (string, error) {
	return "", nil
}

func (c *OrgDashboardConnector) SendUserNotification(ctx context.Context, message, _ string, dest *NotificationDestination) (string, error) {
	if dest == nil || dest.Content == nil || dest.Content.Summary == nil {
		return "", nil
	}
	summary := dest.Content.Summary
	kind := strings.ToLower(strings.TrimSpace(summary.Kind))
	if kind != "run_summary" {
		return "", nil
	}
	workspacePath, dbPath, err := orgDashboardDBPath(dest.WorkspacePath)
	if err != nil {
		return "", err
	}
	defer livefeed.PublishWorkflow(livefeed.Notifications, workspacePath)
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return "", fmt.Errorf("create workflow database folder: %w", err)
	}

	fields := notificationFieldsWithRoute(summary.Fields, summary.Route)
	fieldsJSON, err := json.Marshal(fields)
	if err != nil {
		return "", err
	}
	sectionsJSON, err := json.Marshal(summary.Sections)
	if err != nil {
		return "", err
	}
	routes := summary.Routes
	if routes == nil {
		routes = []NotificationRouteSummary{}
	}
	routesJSON, err := json.Marshal(routes)
	if err != nil {
		return "", err
	}
	// Keep message complete for existing reports that read only that column.
	// New views use the lead plus typed route entries without duplicate prose.
	summaryText := ""
	if len(routes) > 0 {
		summaryText = strings.TrimSpace(dest.Content.Text)
	}
	id := uuid.NewString()
	now := time.Now().UTC().Format(time.RFC3339Nano)

	c.mu.Lock()
	defer c.mu.Unlock()
	db, err := openOrgDashboardDB(ctx, dbPath)
	if err != nil {
		return "", err
	}
	defer db.Close()
	if err := ensureOrgDashboardSchema(ctx, db); err != nil {
		return "", err
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO org_dashboard_notifications
		(id, notification_kind, title, status, message, fields_json, sections_json, route_summaries_json, summary_text, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, kind, strings.TrimSpace(summary.Title), normalizedSummaryStatus(summary.Status), strings.TrimSpace(message), string(fieldsJSON), string(sectionsJSON), string(routesJSON), summaryText, now,
	); err != nil {
		return "", fmt.Errorf("persist org dashboard notification for %s: %w", workspacePath, err)
	}
	// Keep fifty per kind AND route, plus the existing workflow stream. A busy
	// route cannot evict another route's last status. One digest remains one row.
	_, _ = db.ExecContext(ctx, `WITH scopes AS (
		SELECT id, notification_kind, created_at, 'overall' AS scope FROM org_dashboard_notifications
		UNION ALL
		SELECT n.id, n.notification_kind, n.created_at,
			json_array('route', json_extract(r.value,'$.routing_step_id'), json_extract(r.value,'$.route_id'))
		FROM org_dashboard_notifications n, json_each(n.route_summaries_json) r
		UNION ALL
		SELECT n.id, n.notification_kind, n.created_at, json_array('legacy', json_extract(f.value,'$.value'))
		FROM org_dashboard_notifications n, json_each(n.fields_json) f
		WHERE json_array_length(n.route_summaries_json)=0 AND lower(trim(json_extract(f.value,'$.label')))='route'
	), ranked AS (
		SELECT id, row_number() OVER (PARTITION BY notification_kind, scope ORDER BY created_at DESC, id DESC) AS rank FROM scopes
	)
	DELETE FROM org_dashboard_notifications WHERE id NOT IN (SELECT id FROM ranked WHERE rank <= ?)`, orgDashboardHistoryLimitPerKind)
	return id, nil
}

func normalizedSummaryStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "failed", "partial", "blocked", "waiting_for_user", "waiting_for_platform", "monitoring", "informational", "no_run":
		return strings.ToLower(strings.TrimSpace(status))
	// Older notifications used visual severity. Preserve their meaning as a
	// truthful workflow status when they are read or rewritten.
	case "success":
		return "completed"
	case "warning":
		return "blocked"
	case "danger":
		return "failed"
	default:
		return "informational"
	}
}

func orgDashboardDBPath(rawWorkspacePath string) (string, string, error) {
	workspacePath := filepath.ToSlash(filepath.Clean(strings.Trim(strings.TrimSpace(rawWorkspacePath), "/")))
	if workspacePath == "." || workspacePath == "" || filepath.IsAbs(workspacePath) {
		return "", "", fmt.Errorf("org_dashboard requires a workflow workspace path")
	}
	for _, part := range strings.Split(workspacePath, "/") {
		if part == ".." {
			return "", "", fmt.Errorf("workspace path escapes workspace root")
		}
	}
	root, err := filepath.Abs(fsutil.WorkspaceDocsRoot())
	if err != nil {
		return "", "", err
	}
	dbPath, err := fsutil.WorkspacePath(root, workspacePath+"/db/db.sqlite")
	if err != nil {
		return "", "", err
	}
	return workspacePath, dbPath, nil
}

func openOrgDashboardDB(ctx context.Context, dbPath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	if _, err := db.ExecContext(ctx, `PRAGMA busy_timeout = 5000`); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func ensureOrgDashboardSchema(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS org_dashboard_notifications (
			id TEXT PRIMARY KEY,
			notification_kind TEXT NOT NULL CHECK (notification_kind IN ('run_summary')),
			title TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'informational',
			message TEXT NOT NULL,
			fields_json TEXT NOT NULL DEFAULT '[]',
			sections_json TEXT NOT NULL DEFAULT '[]',
			created_at TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_org_dashboard_notifications_kind_created
		ON org_dashboard_notifications(notification_kind, created_at DESC);
	`)
	if err != nil {
		return fmt.Errorf("initialize org dashboard notification store: %w", err)
	}
	for _, column := range []struct{ name, ddl string }{
		{"route_summaries_json", `ALTER TABLE org_dashboard_notifications ADD COLUMN route_summaries_json TEXT NOT NULL DEFAULT '[]'`},
		{"summary_text", `ALTER TABLE org_dashboard_notifications ADD COLUMN summary_text TEXT NOT NULL DEFAULT ''`},
	} {
		var exists int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info('org_dashboard_notifications') WHERE name=?`, column.name).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			if _, err := db.ExecContext(ctx, column.ddl); err != nil {
				// Another reader/writer may have performed the additive migration.
				if checkErr := db.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info('org_dashboard_notifications') WHERE name=?`, column.name).Scan(&exists); checkErr != nil || exists == 0 {
					return fmt.Errorf("add notification %s: %w", column.name, err)
				}
			}
		}
	}
	return nil
}

func ListOrgDashboardNotifications(ctx context.Context, rawWorkspacePath string, recentLimit int) (OrgDashboardWorkflowNotifications, error) {
	workspacePath, dbPath, err := orgDashboardDBPath(rawWorkspacePath)
	if err != nil {
		return OrgDashboardWorkflowNotifications{}, err
	}
	result := OrgDashboardWorkflowNotifications{WorkspacePath: workspacePath}
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			return result, nil
		}
		return result, err
	}
	db, err := openOrgDashboardDB(ctx, dbPath)
	if err != nil {
		return result, err
	}
	defer db.Close()
	if err := ensureOrgDashboardSchema(ctx, db); err != nil {
		return result, err
	}
	if recentLimit <= 0 || recentLimit > 50 {
		recentLimit = 10
	}
	// Read the bounded retained store, rather than deriving per-route latest
	// from the caller's recent limit (which could contain only a busy route).
	rows, err := db.QueryContext(ctx, `
		SELECT id, notification_kind, title, status, message, fields_json, sections_json, route_summaries_json, summary_text, created_at
		FROM org_dashboard_notifications
		ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	byRoute := map[[3]string]int{}
	for rows.Next() {
		var item OrgDashboardNotification
		var fieldsJSON, sectionsJSON, routesJSON, summaryText string
		item.WorkspacePath = workspacePath
		if err := rows.Scan(&item.ID, &item.Kind, &item.Title, &item.Status, &item.Message, &fieldsJSON, &sectionsJSON, &routesJSON, &summaryText, &item.CreatedAt); err != nil {
			return result, err
		}
		_ = json.Unmarshal([]byte(fieldsJSON), &item.Fields)
		item.Route = notificationRouteFromFields(item.Fields)
		item.Fields = notificationFieldsWithoutRoute(item.Fields)
		_ = json.Unmarshal([]byte(sectionsJSON), &item.Sections)
		if err := json.Unmarshal([]byte(routesJSON), &item.Routes); err != nil {
			return result, fmt.Errorf("read route summaries for notification %s: %w", item.ID, err)
		}

		if len(item.Routes) > 0 && summaryText != "" {
			item.Message = summaryText
		}
		item.Status = normalizedSummaryStatus(item.Status)
		if item.Kind == "run_summary" && result.RunSummary == nil {
			copy := item
			result.RunSummary = &copy
		}
		if len(result.Recent) < recentLimit {
			result.Recent = append(result.Recent, item)
		}
		addRoute := func(key [3]string, group OrgDashboardRouteNotifications, summary OrgDashboardNotification) {
			index, exists := byRoute[key]
			if !exists {
				index = len(result.ByRoute)
				byRoute[key] = index
				result.ByRoute = append(result.ByRoute, group)
			}
			if summary.Kind == "run_summary" && result.ByRoute[index].RunSummary == nil {
				result.ByRoute[index].RunSummary = &summary
			}
		}
		for _, route := range item.Routes {
			label := route.Label
			if label == "" {
				label = route.RouteID
			}
			projected := item
			projected.Route, projected.Title, projected.Status, projected.Message = label, route.Title, normalizedSummaryStatus(route.Status), route.Message
			projected.Fields, projected.Sections, projected.Routes = route.Fields, route.Sections, nil
			addRoute([3]string{"route", route.RoutingStepID, route.RouteID}, OrgDashboardRouteNotifications{
				RoutingStepID: route.RoutingStepID, RouteID: route.RouteID, Label: label,
			}, projected)
		}
		if len(item.Routes) == 0 && item.Route != "" {
			// Preserve old labels without guessing their router or merging them
			// into canonical identities they never recorded.
			addRoute([3]string{"legacy", "", item.Route}, OrgDashboardRouteNotifications{RouteID: item.Route, Label: item.Route, Legacy: true}, item)
		}
	}
	return result, rows.Err()
}

func notificationFieldsWithRoute(fields []NotificationSummaryField, route string) []NotificationSummaryField {
	route = strings.TrimSpace(route)
	result := append([]NotificationSummaryField(nil), fields...)
	for i := range result {
		if strings.EqualFold(strings.TrimSpace(result[i].Label), "route") {
			if route != "" {
				result[i].Value = route
			}
			return result
		}
	}
	if route == "" {
		return result
	}
	return append([]NotificationSummaryField{{Label: "Route", Value: route}}, result...)
}

func notificationRouteFromFields(fields []NotificationSummaryField) string {
	for _, field := range fields {
		if strings.EqualFold(strings.TrimSpace(field.Label), "route") {
			return strings.TrimSpace(field.Value)
		}
	}
	return ""
}

func notificationFieldsWithoutRoute(fields []NotificationSummaryField) []NotificationSummaryField {
	result := make([]NotificationSummaryField, 0, len(fields))
	for _, field := range fields {
		if !strings.EqualFold(strings.TrimSpace(field.Label), "route") {
			result = append(result, field)
		}
	}
	return result
}
