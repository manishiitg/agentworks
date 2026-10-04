// Package gmailsetup provisions only the Google resources in a reviewed Gmail plan.
// It never holds a refresh token or grants a principal project-wide roles.
package gmailsetup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type Plan struct {
	ClientName         string `json:"client_name"`
	ProjectID          string `json:"project_id"`
	DeliveryProjectID  string `json:"delivery_project_id"`
	OAuthProjectNumber string `json:"oauth_project_number"`
	Endpoint           string `json:"push_endpoint"`
	Topic              string `json:"topic"`
	Subscription       string `json:"subscription"`
	PushEmail          string `json:"push_service_account"`
}

var projectID = regexp.MustCompile(`^[a-z][a-z0-9-]{4,28}[a-z0-9]$`)
var googleClientID = regexp.MustCompile(`^([0-9]+)-[A-Za-z0-9]+\.apps\.googleusercontent\.com$`)

// NewPlan validates operator input and derives stable names: a shared project
// topic, but distinct delivery resources for each deployment URL.
func NewPlan(client, id, project, endpoint, topic, pushEmail string) (Plan, error) {
	p := Plan{ClientName: client, ProjectID: project, DeliveryProjectID: project, Endpoint: endpoint}
	m := googleClientID.FindStringSubmatch(id)
	if !projectID.MatchString(project) || len(m) != 2 || client == "" {
		return p, fmt.Errorf("select a registered Google OAuth client and its Google Cloud project ID")
	}
	p.OAuthProjectNumber = m[1]
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "/api/hooks/gmail/events" || u.RawQuery != "" || u.Fragment != "" {
		return p, fmt.Errorf("a public HTTPS Gmail event URL is required; local development needs its existing tunnel URL")
	}
	digest := sha256.Sum256([]byte(endpoint))
	suffix := hex.EncodeToString(digest[:6])
	p.Topic = "projects/" + project + "/topics/agentworks-gmail"
	if topic != "" {
		prefix := "projects/" + project + "/topics/"
		if !strings.HasPrefix(topic, prefix) || !regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._~%+-]{2,254}$`).MatchString(strings.TrimPrefix(topic, prefix)) {
			return p, fmt.Errorf("existing topic belongs to a different OAuth project or is invalid; setup will not replace it")
		}
		p.Topic = topic
	}
	p.PushEmail = "aw-gmail-" + suffix + "@" + project + ".iam.gserviceaccount.com"
	if pushEmail != "" {
		parts := strings.Split(pushEmail, "@")
		if len(parts) != 2 || !strings.HasSuffix(parts[1], ".iam.gserviceaccount.com") || !regexp.MustCompile(`^[a-z][a-z0-9-]{4,28}[a-z0-9]$`).MatchString(parts[0]) {
			return p, fmt.Errorf("existing push service account is invalid")
		}
		p.DeliveryProjectID = strings.TrimSuffix(parts[1], ".iam.gserviceaccount.com")
		if !projectID.MatchString(p.DeliveryProjectID) {
			return p, fmt.Errorf("invalid delivery project")
		}
		p.PushEmail = pushEmail
	}
	p.Subscription = "projects/" + p.DeliveryProjectID + "/subscriptions/aw-gmail-" + suffix + "-" + project
	return p, nil
}

// Cloud's client authenticates with the administrator's short-lived consent
// token. URLs are fixed Google API hosts, never supplied by an agent.
type Cloud struct{ Client *http.Client }

func pubsubURL(resource string) string {
	parts := strings.Split(resource, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return "https://pubsub.googleapis.com/v1/" + strings.Join(parts, "/")
}

type APIError struct {
	Status    int
	Operation string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("Google Cloud %s returned HTTP %d. Check project permissions, API enablement and organization policy; ask Builder to retry setup after fixing access.", e.Operation, e.Status)
}

func (c Cloud) request(ctx context.Context, method, endpoint string, input, output any) error {
	var body io.Reader
	if input != nil {
		b, e := json.Marshal(input)
		if e != nil {
			return e
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Client.Do(req)
	if err != nil {
		return fmt.Errorf("Google Cloud request did not complete; retry setup")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Do not expose Google's response body: it can echo OAuth tokens or input.
		return &APIError{Status: resp.StatusCode, Operation: req.URL.Host + req.URL.Path}
	}
	if output != nil {
		return json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(output)
	}
	return nil
}

func missing(err error) bool {
	var e *APIError
	return errors.As(err, &e) && e.Status == http.StatusNotFound
}

type operation struct {
	Name  string `json:"name"`
	Done  bool   `json:"done"`
	Error *struct {
		Code int `json:"code"`
	} `json:"error"`
	Response json.RawMessage `json:"response"`
}

func (c Cloud) await(ctx context.Context, host string, op operation) error {
	for !op.Done {
		if !regexp.MustCompile(`^operations/[A-Za-z0-9/_-]+$`).MatchString(op.Name) {
			return fmt.Errorf("Google returned an invalid setup operation")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
		if err := c.request(ctx, "GET", host+"/"+op.Name, nil, &op); err != nil {
			return err
		}
	}
	if op.Error != nil {
		return fmt.Errorf("Google Cloud operation failed (code %d); check project permissions and organization policy, then retry", op.Error.Code)
	}
	return nil
}

func (c Cloud) enable(ctx context.Context, project string, apis []string) error {
	var op operation
	if e := c.request(ctx, "POST", "https://serviceusage.googleapis.com/v1/projects/"+project+"/services:batchEnable", map[string]any{"serviceIds": apis}, &op); e != nil {
		return e
	}
	return c.await(ctx, "https://serviceusage.googleapis.com/v1", op)
}

// grant merges exactly one unconditional binding, preserving etags, unrelated
// grants and conditional bindings. Conflicting etags fail rather than overwrite.
func (c Cloud) grant(ctx context.Context, resource, role, member string) error {
	policy := map[string]any{}
	getURL := resource + ":getIamPolicy"
	var getBody any = map[string]any{"options": map[string]int{"requestedPolicyVersion": 3}}
	if strings.HasPrefix(resource, "https://iam.googleapis.com/") {
		getURL += "?options.requestedPolicyVersion=3"
		getBody = nil
	}
	if e := c.request(ctx, "POST", getURL, getBody, &policy); e != nil {
		return e
	}
	bindings, _ := policy["bindings"].([]any)
	for _, raw := range bindings {
		b, ok := raw.(map[string]any)
		if !ok || b["role"] != role || b["condition"] != nil {
			continue
		}
		members, _ := b["members"].([]any)
		for _, value := range members {
			if value == member {
				return nil
			}
		}
		b["members"] = append(members, member)
		return c.request(ctx, "POST", resource+":setIamPolicy", map[string]any{"policy": policy}, nil)
	}
	policy["bindings"] = append(bindings, map[string]any{"role": role, "members": []string{member}})
	return c.request(ctx, "POST", resource+":setIamPolicy", map[string]any{"policy": policy}, nil)
}

func (c Cloud) project(ctx context.Context, id string) (string, error) {
	var p struct{ Name, ProjectID, State string }
	if e := c.request(ctx, "GET", "https://cloudresourcemanager.googleapis.com/v3/projects/"+id, nil, &p); e != nil {
		return "", e
	}
	if p.ProjectID != id || p.State != "ACTIVE" || !regexp.MustCompile(`^projects/[0-9]+$`).MatchString(p.Name) {
		return "", fmt.Errorf("Google Cloud project is not active or its identity could not be verified")
	}
	return strings.TrimPrefix(p.Name, "projects/"), nil
}

func (c Cloud) subscription(ctx context.Context, p Plan, create bool) error {
	endpoint := pubsubURL(p.Subscription)
	var sub struct {
		Topic      string `json:"topic"`
		Filter     string `json:"filter"`
		PushConfig struct {
			PushEndpoint string                                         `json:"pushEndpoint"`
			OIDCToken    struct{ ServiceAccountEmail, Audience string } `json:"oidcToken"`
			NoWrapper    *json.RawMessage                               `json:"noWrapper"`
		} `json:"pushConfig"`
	}
	err := c.request(ctx, "GET", endpoint, nil, &sub)
	if missing(err) && create {
		return c.request(ctx, "PUT", endpoint, map[string]any{
			"topic": p.Topic, "ackDeadlineSeconds": 30, "expirationPolicy": map[string]any{},
			"pushConfig": map[string]any{"pushEndpoint": p.Endpoint, "oidcToken": map[string]string{"serviceAccountEmail": p.PushEmail, "audience": p.Endpoint}},
		}, nil)
	}
	if err != nil {
		return err
	}
	if sub.Topic != p.Topic || sub.PushConfig.PushEndpoint != p.Endpoint || sub.PushConfig.OIDCToken.ServiceAccountEmail != p.PushEmail || sub.PushConfig.OIDCToken.Audience != p.Endpoint || sub.PushConfig.NoWrapper != nil || sub.Filter != "" {
		return fmt.Errorf("existing subscription differs from the reviewed plan; it has not been replaced")
	}
	return nil
}

func (c Cloud) Provision(ctx context.Context, p Plan, progress func(string)) error {
	progress("Verifying OAuth project")
	number, err := c.project(ctx, p.ProjectID)
	if err != nil {
		return err
	}
	if number != p.OAuthProjectNumber {
		return fmt.Errorf("the selected project does not own this OAuth client; no resources were changed")
	}
	deliveryNumber := number
	if p.DeliveryProjectID != p.ProjectID {
		deliveryNumber, err = c.project(ctx, p.DeliveryProjectID)
		if err != nil {
			return err
		}
	}
	progress("Enabling Google APIs")
	if err = c.enable(ctx, p.ProjectID, []string{"gmail.googleapis.com", "pubsub.googleapis.com", "iam.googleapis.com"}); err != nil {
		return err
	}
	if p.DeliveryProjectID != p.ProjectID {
		if err = c.enable(ctx, p.DeliveryProjectID, []string{"pubsub.googleapis.com", "iam.googleapis.com"}); err != nil {
			return err
		}
	}
	// Refuse a conflicting existing subscription before changing its IAM or topic.
	if err = c.subscription(ctx, p, false); err != nil && !missing(err) {
		return err
	}
	progress("Preparing authenticated delivery")
	var op operation
	if err = c.request(ctx, "POST", "https://serviceusage.googleapis.com/v1beta1/projects/"+deliveryNumber+"/services/pubsub.googleapis.com:generateServiceIdentity", map[string]any{}, &op); err != nil {
		return err
	}
	if err = c.await(ctx, "https://serviceusage.googleapis.com/v1beta1", op); err != nil {
		return err
	}
	sa := "https://iam.googleapis.com/v1/projects/" + p.DeliveryProjectID + "/serviceAccounts/" + p.PushEmail
	if err = c.request(ctx, "GET", sa, nil, &map[string]any{}); missing(err) {
		err = c.request(ctx, "POST", "https://iam.googleapis.com/v1/projects/"+p.DeliveryProjectID+"/serviceAccounts", map[string]any{"accountId": strings.Split(p.PushEmail, "@")[0], "serviceAccount": map[string]string{"displayName": "AgentWorks Gmail push"}}, nil)
	}
	if err != nil {
		return err
	}
	if err = c.grant(ctx, sa, "roles/iam.serviceAccountTokenCreator", "serviceAccount:service-"+deliveryNumber+"@gcp-sa-pubsub.iam.gserviceaccount.com"); err != nil {
		return err
	}
	progress("Preparing Gmail topic")
	topic := pubsubURL(p.Topic)
	if err = c.request(ctx, "GET", topic, nil, &map[string]any{}); missing(err) {
		err = c.request(ctx, "PUT", topic, map[string]any{}, nil)
	}
	if err != nil {
		return err
	}
	if err = c.grant(ctx, topic, "roles/pubsub.publisher", "serviceAccount:gmail-api-push@system.gserviceaccount.com"); err != nil {
		return err
	}
	progress("Creating and verifying push subscription")
	if err = c.subscription(ctx, p, true); err != nil {
		return err
	}
	return c.subscription(ctx, p, false)
}
