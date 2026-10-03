package store

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

var auditIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type clickHouseAudit struct {
	endpoint                 *url.URL
	client                   *http.Client
	database, user, password string
	retention                time.Duration
	spool                    *sqliteAudit
	stop, done               chan struct{}
	closeOnce                sync.Once
	closeErr                 error
}

func openClickHouseAudit(o AuditOptions) (*clickHouseAudit, error) {
	u, err := url.Parse(o.ClickHouseURL)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("VAULT_AUDIT_CLICKHOUSE_URL must be an HTTP(S) URL without credentials or query")
	}
	ip := net.ParseIP(u.Hostname())
	loopback := u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())
	if u.Scheme != "https" && !loopback {
		return nil, errors.New("ClickHouse requires HTTPS except on loopback")
	}
	database := o.ClickHouseDatabase
	if database == "" {
		database = "default"
	}
	if !auditIdentifier.MatchString(database) {
		return nil, errors.New("invalid ClickHouse database identifier")
	}
	a := &clickHouseAudit{endpoint: u, client: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, database: database, user: o.ClickHouseUser, password: o.ClickHousePassword, retention: o.Retention, stop: make(chan struct{}), done: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ttl := "toDateTime(intDiv(TimestampMs,1000)) + toIntervalSecond(" + strconv.FormatInt(int64(o.Retention/time.Second), 10) + ")"
	schema := `CREATE TABLE IF NOT EXISTS ` + a.table() + ` (
 ID String, WorkspaceID String, TimestampMs Int64, UserID String, ClientID String,
 ConnectorID String, PublicName String, Decision LowCardinality(String), Outcome LowCardinality(String),
 DurationMs Int64, GroupIDs Array(String), Event String
 ) ENGINE=ReplacingMergeTree PARTITION BY toYYYYMM(toDateTime(intDiv(TimestampMs,1000)))
 ORDER BY (WorkspaceID,TimestampMs,ID) TTL ` + ttl
	if _, err = a.request(ctx, schema, nil, nil); err != nil {
		return nil, fmt.Errorf("initialize ClickHouse audit table: %w", err)
	}
	if _, err = a.request(ctx, "ALTER TABLE "+a.table()+" MODIFY TTL "+ttl, nil, nil); err != nil {
		return nil, fmt.Errorf("configure ClickHouse audit retention: %w", err)
	}
	a.spool, err = openSQLiteAuditMode(filepath.Join(o.StateDir, "audit-clickhouse-queue.sqlite"), 0, o.MaxBytes, false, o.WriteMode)
	if err != nil {
		return nil, err
	}
	go a.deliver()
	return a, nil
}
func (a *clickHouseAudit) table() string { return a.database + ".vault_audit_events" }
func (a *clickHouseAudit) Info() AuditInfo {
	info := a.spool.Info()
	info.Provider = "clickhouse"
	info.RetentionSeconds = int64(a.retention / time.Second)
	return info
}
func (a *clickHouseAudit) Append(ctx context.Context, e AuditEvent) error {
	return a.spool.Append(ctx, e)
}
func (a *clickHouseAudit) request(ctx context.Context, query string, params url.Values, payload []byte) ([]byte, error) {
	u := *a.endpoint
	q := u.Query()
	q.Set("wait_end_of_query", "1")
	q.Set("output_format_json_quote_64bit_integers", "0")
	for k, vs := range params {
		for _, v := range vs {
			q.Add(k, v)
		}
	}
	var body io.Reader
	if payload != nil {
		q.Set("query", query)
		body = bytes.NewReader(payload)
	} else {
		body = strings.NewReader(query)
	}
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), body)
	if err != nil {
		return nil, errors.New("invalid ClickHouse request")
	}
	if a.user != "" {
		req.Header.Set("X-ClickHouse-User", a.user)
	}
	if a.password != "" {
		req.Header.Set("X-ClickHouse-Key", a.password)
	}
	req.Header.Set("Content-Type", "text/plain")
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, errors.New("ClickHouse audit service unreachable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("ClickHouse audit service returned HTTP %d", resp.StatusCode)
	}
	const max = 32 << 20
	data, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, errors.New("ClickHouse audit response incomplete")
	}
	if len(data) > max {
		return nil, errors.New("audit response too large; narrow filters")
	}
	return data, nil
}
func (a *clickHouseAudit) flush(ctx context.Context) (bool, error) {
	events, err := a.spool.pending(ctx, 1000)
	if err != nil || len(events) == 0 {
		return false, err
	}
	var body bytes.Buffer
	enc := json.NewEncoder(&body)
	for _, e := range events {
		raw, err := json.Marshal(e)
		if err != nil {
			return false, err
		}
		groups := e.GroupIDs
		if groups == nil {
			groups = []string{}
		}
		row := map[string]any{"ID": e.ID, "WorkspaceID": e.WorkspaceID, "TimestampMs": e.Timestamp.UnixMilli(), "UserID": e.UserID, "ClientID": e.ClientID, "ConnectorID": e.ConnectorID, "PublicName": e.PublicName, "Decision": e.Decision, "Outcome": e.Outcome, "DurationMs": e.DurationMs, "GroupIDs": groups, "Event": string(raw)}
		if err = enc.Encode(row); err != nil {
			return false, err
		}
	}
	if _, err = a.request(ctx, "INSERT INTO "+a.table()+" FORMAT JSONEachRow", nil, body.Bytes()); err != nil {
		return false, err
	}
	// Stable IDs and FINAL deduplicate replay after crashes/ambiguous responses.
	return len(events) == 1000, a.spool.delivered(ctx, events)
}
func (a *clickHouseAudit) deliver() {
	defer close(a.done)
	delay := time.Second
	for {
		timer := time.NewTimer(delay)
		select {
		case <-a.stop:
			timer.Stop()
			return
		case <-timer.C:
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		more, err := a.flush(ctx)
		cancel()
		if err != nil {
			if delay == time.Second || delay >= 30*time.Second {
				log.Print("Vault audit delivery delayed; durable events remain queued")
			}
			delay *= 2
			if delay > 30*time.Second {
				delay = 30 * time.Second
			}
			continue
		}
		delay = time.Second
		if more {
			delay = time.Millisecond
		}
	}
}
func (a *clickHouseAudit) where(f AuditFilter) (string, url.Values) {
	parts := []string{"WorkspaceID = {workspace:String}"}
	p := url.Values{"param_workspace": {f.WorkspaceID}}
	for _, v := range []struct{ column, name, value string }{{"UserID", "user", f.UserID}, {"ClientID", "client", f.ClientID}, {"ConnectorID", "connector", f.ConnectorID}, {"Decision", "decision", f.Decision}, {"Outcome", "outcome", f.Outcome}} {
		if v.value != "" {
			parts = append(parts, v.column+" = {"+v.name+":String}")
			p.Set("param_"+v.name, v.value)
		}
	}
	if f.GroupID != "" {
		parts = append(parts, "has(GroupIDs,{group:String})")
		p.Set("param_group", f.GroupID)
	}
	if f.PublicName != "" {
		parts = append(parts, "positionCaseInsensitiveUTF8(PublicName,{tool:String}) > 0")
		p.Set("param_tool", f.PublicName)
	}
	after := f.After
	cutoff := time.Now().Add(-a.retention)
	if after.Before(cutoff) {
		after = cutoff
	}
	parts = append(parts, "TimestampMs >= {after:Int64}")
	p.Set("param_after", strconv.FormatInt(after.UnixMilli(), 10))
	if !f.Before.IsZero() {
		parts = append(parts, "TimestampMs <= {before:Int64}")
		p.Set("param_before", strconv.FormatInt(f.Before.UnixMilli(), 10))
	}
	return " WHERE " + strings.Join(parts, " AND "), p
}
func (a *clickHouseAudit) Query(ctx context.Context, f AuditFilter) ([]AuditEvent, error) {
	where, p := a.where(f)
	q := "SELECT Event FROM " + a.table() + " FINAL" + where + " ORDER BY TimestampMs DESC, ID DESC"
	if f.Limit > 0 {
		q += " LIMIT {limit:UInt64}"
		p.Set("param_limit", strconv.Itoa(f.Limit))
	}
	data, err := a.request(ctx, q+" FORMAT JSONEachRow", p, nil)
	if err != nil {
		return nil, err
	}
	out := []AuditEvent{}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		var row struct{ Event string }
		var e AuditEvent
		if err = json.Unmarshal(scanner.Bytes(), &row); err != nil {
			return nil, errors.New("invalid ClickHouse audit response")
		}
		if err = json.Unmarshal([]byte(row.Event), &e); err != nil {
			return nil, errors.New("invalid stored audit event")
		}
		out = append(out, e)
	}
	return out, scanner.Err()
}
func (a *clickHouseAudit) Summary(ctx context.Context, f AuditFilter) (AuditSummary, error) {
	where, p := a.where(f)
	out := emptyAuditSummary()
	q := `SELECT count() AS Total,countIf(Decision='allow') AS Allowed,countIf(Decision!='allow') AS Denied,countIf(Outcome='upstream_error') AS UpstreamErrors,toInt64(if(count()=0,0,avg(DurationMs))) AS AvgDurationMs FROM ` + a.table() + " FINAL" + where + " FORMAT JSONEachRow"
	data, err := a.request(ctx, q, p, nil)
	if err != nil {
		return out, err
	}
	if err = json.Unmarshal(bytes.TrimSpace(data), &out); err != nil {
		return out, errors.New("invalid ClickHouse audit summary")
	}
	out.ByDay = []UsageBucket{}
	out.ByTool = []UsageBucket{}
	for _, g := range []struct {
		expr, order string
		limit       int
		dest        *[]UsageBucket
	}{{"formatDateTime(toDateTime(intDiv(TimestampMs,1000)),'%Y-%m-%d','UTC')", "Key DESC", 14, &out.ByDay}, {"PublicName", "Count DESC, Key ASC", 10, &out.ByTool}} {
		q := "SELECT " + g.expr + " AS Key,count() AS Count,countIf(Decision!='allow') AS Denied,countIf(Outcome='upstream_error') AS UpstreamErrors FROM " + a.table() + " FINAL" + where + " GROUP BY Key ORDER BY " + g.order + fmt.Sprintf(" LIMIT %d FORMAT JSONEachRow", g.limit)
		data, err = a.request(ctx, q, p, nil)
		if err != nil {
			return out, err
		}
		scanner := bufio.NewScanner(bytes.NewReader(data))
		scanner.Buffer(make([]byte, 4096), 1<<20)
		for scanner.Scan() {
			var b UsageBucket
			if err = json.Unmarshal(scanner.Bytes(), &b); err != nil {
				return out, errors.New("invalid ClickHouse audit buckets")
			}
			*g.dest = append(*g.dest, b)
		}
		if err = scanner.Err(); err != nil {
			return out, err
		}
	}
	return out, nil
}
func (a *clickHouseAudit) Close() error {
	a.closeOnce.Do(func() { close(a.stop); <-a.done; a.closeErr = a.spool.Close() })
	return a.closeErr
}
