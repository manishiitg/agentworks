package gmailsetup

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type fakeCloud struct {
	requests      []*http.Request
	bodies        []map[string]any
	projectNumber string
	subscription  map[string]any
	policy        map[string]any
	savedPolicies []map[string]any
	newResources  bool
	failPath      string
}

func (f *fakeCloud) RoundTrip(r *http.Request) (*http.Response, error) {
	f.requests = append(f.requests, r)
	body := map[string]any{}
	if r.Body != nil {
		raw, _ := io.ReadAll(r.Body)
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &body); err != nil {
				panic(err)
			}
		}
	}
	f.bodies = append(f.bodies, body)
	status, data := 200, any(map[string]any{})
	p := r.URL.Path
	switch {
	case strings.Contains(p, f.failPath) && f.failPath != "":
		status, data = 403, map[string]string{"error": "access_token=SECRET should never be returned"}
	case r.URL.Host == "cloudresourcemanager.googleapis.com":
		number := f.projectNumber
		if number == "" {
			number = "123456"
		}
		if strings.HasSuffix(p, "delivery-project") {
			number = "999999"
		}
		data = map[string]string{"name": "projects/" + number, "projectId": strings.TrimPrefix(p, "/v3/projects/"), "state": "ACTIVE"}
	case strings.HasSuffix(p, ":batchEnable") || strings.HasSuffix(p, ":generateServiceIdentity"):
		data = map[string]any{"done": true}
	case strings.Contains(p, "/subscriptions/"):
		if r.Method == "PUT" {
			f.subscription = body
			data = body
		} else if f.subscription == nil {
			status = 404
		} else {
			data = f.subscription
		}
	case strings.HasSuffix(p, ":getIamPolicy"):
		if f.policy != nil {
			data = f.policy
		} else {
			data = map[string]any{"etag": "original-etag", "version": 3, "bindings": []any{map[string]any{"role": "roles/viewer", "members": []string{"user:other@example.com"}}, map[string]any{"role": "roles/pubsub.publisher", "members": []string{"user:conditional@example.com"}, "condition": map[string]string{"title": "limited", "expression": "true"}}}}
		}
	case strings.HasSuffix(p, ":setIamPolicy"):
		f.savedPolicies = append(f.savedPolicies, body["policy"].(map[string]any))
	case f.newResources && r.Method == "GET" && (strings.Contains(p, "/serviceAccounts/") || strings.Contains(p, "/topics/")):
		status = 404
	}
	raw, _ := json.Marshal(data)
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(string(raw))), Header: make(http.Header), Request: r}, nil
}

func plan(t *testing.T) Plan {
	t.Helper()
	p, err := NewPlan("company", "123456-abcdef.apps.googleusercontent.com", "rts-project", "https://video.realtrainingsys.com/api/hooks/gmail/events", "", "")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func cloud(f *fakeCloud) Cloud { return Cloud{Client: &http.Client{Transport: f}} }

func TestProvisionCreatesAndVerifiesOnlyReviewedResources(t *testing.T) {
	p := plan(t)
	f := &fakeCloud{newResources: true}
	stages := []string{}
	if err := cloud(f).Provision(context.Background(), p, func(s string) { stages = append(stages, s) }); err != nil {
		t.Fatal(err)
	}
	if len(stages) < 5 || len(f.savedPolicies) != 2 {
		t.Fatalf("missing steps or grants: %v %v", stages, f.savedPolicies)
	}
	for _, policy := range f.savedPolicies {
		if policy["etag"] != "original-etag" || policy["version"] != float64(3) {
			t.Fatalf("lost IAM etag/version: %v", policy)
		}
		bindings := policy["bindings"].([]any)
		if len(bindings) != 3 || bindings[1].(map[string]any)["condition"] == nil {
			t.Fatalf("lost original/conditional grants: %v", bindings)
		}
	}
	if f.subscription["topic"] != p.Topic {
		t.Fatal("wrong topic")
	}
	push := f.subscription["pushConfig"].(map[string]any)
	if push["pushEndpoint"] != p.Endpoint || push["noWrapper"] != nil || push["oidcToken"].(map[string]any)["audience"] != p.Endpoint {
		t.Fatalf("incorrect push authentication: %v", push)
	}
	for i, r := range f.requests {
		if r.URL.Host == "iam.googleapis.com" && strings.HasSuffix(r.URL.Path, ":getIamPolicy") && (len(f.bodies[i]) != 0 || r.URL.Query().Get("options.requestedPolicyVersion") != "3") {
			t.Fatalf("IAM get-policy wire format: %s %v", r.URL, f.bodies[i])
		}
	}
}

func TestProvisionWrongOAuthProjectDoesNotMutate(t *testing.T) {
	f := &fakeCloud{projectNumber: "654321"}
	if err := cloud(f).Provision(context.Background(), plan(t), func(string) {}); err == nil || !strings.Contains(err.Error(), "does not own") {
		t.Fatalf("wrong project: %v", err)
	}
	for _, r := range f.requests {
		if r.Method != "GET" {
			t.Fatalf("mutated wrong project: %s", r.URL)
		}
	}
}

func TestProvisionRefusesConflictingExistingSubscription(t *testing.T) {
	f := &fakeCloud{subscription: map[string]any{"topic": plan(t).Topic, "pushConfig": map[string]string{"pushEndpoint": "https://another-deployment.example/hook"}}}
	if err := cloud(f).Provision(context.Background(), plan(t), func(string) {}); err == nil || !strings.Contains(err.Error(), "not been replaced") {
		t.Fatalf("conflict: %v", err)
	}
	for _, r := range f.requests {
		if strings.Contains(r.URL.Path, "serviceAccounts") || strings.HasSuffix(r.URL.Path, ":setIamPolicy") || r.Method == "PUT" {
			t.Fatalf("changed conflicting delivery: %s", r.URL)
		}
	}
}

func TestProvisionRetryReusesResourcesAndNarrowIAMGrants(t *testing.T) {
	f := &fakeCloud{newResources: false}
	if err := cloud(f).Provision(context.Background(), plan(t), func(string) {}); err != nil {
		t.Fatal(err)
	}
	p := plan(t)
	f.policy = map[string]any{"etag": "retry", "bindings": []any{
		map[string]any{"role": "roles/pubsub.publisher", "members": []string{"serviceAccount:gmail-api-push@system.gserviceaccount.com"}},
		map[string]any{"role": "roles/iam.serviceAccountTokenCreator", "members": []string{"serviceAccount:service-123456@gcp-sa-pubsub.iam.gserviceaccount.com"}},
	}}
	f.requests = nil
	f.bodies = nil
	f.savedPolicies = nil
	if err := cloud(f).Provision(context.Background(), p, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if len(f.savedPolicies) != 0 {
		t.Fatal("rewrote matching IAM grants")
	}
	for _, r := range f.requests {
		if r.Method == "PUT" || strings.HasSuffix(r.URL.Path, "/serviceAccounts") {
			t.Fatalf("recreated matching resource: %s", r.URL)
		}
	}
}

func TestProvisionCrossProjectUsesExistingDeliveryIdentity(t *testing.T) {
	p, err := NewPlan("second", "123456-abcdef.apps.googleusercontent.com", "rts-project", "https://video.realtrainingsys.com/api/hooks/gmail/events", "", "existing-push@delivery-project.iam.gserviceaccount.com")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeCloud{}
	if err = cloud(f).Provision(context.Background(), p, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(p.Subscription, "projects/delivery-project/") {
		t.Fatal("subscription is in the wrong identity project")
	}
	raw, _ := json.Marshal(f.savedPolicies)
	if !strings.Contains(string(raw), "service-999999@gcp-sa-pubsub.iam.gserviceaccount.com") {
		t.Fatal("granted wrong service agent")
	}
}

func TestCloudErrorsDoNotReturnCredentialBearingBody(t *testing.T) {
	f := &fakeCloud{failPath: "services:batchEnable"}
	err := cloud(f).Provision(context.Background(), plan(t), func(string) {})
	if err == nil || !strings.Contains(err.Error(), "HTTP 403") || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("unsafe error: %v", err)
	}
}

func TestProvisionEscapesExistingTopicNameAsLiteralResource(t *testing.T) {
	p := plan(t)
	p.Topic = "projects/rts-project/topics/mail%2Btopic"
	f := &fakeCloud{}
	if err := cloud(f).Provision(context.Background(), p, func(string) {}); err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, r := range f.requests {
		if strings.Contains(r.URL.Path, "/topics/") {
			seen = true
			if !strings.Contains(r.URL.EscapedPath(), "mail%252Btopic") {
				t.Fatalf("interpreted percent sequence in topic name: %s", r.URL)
			}
		}
	}
	if !seen {
		t.Fatal("topic was not verified")
	}
}

func TestPlanStableNamesAndRejectsUnsafeInputs(t *testing.T) {
	p := plan(t)
	again := plan(t)
	if p != again {
		t.Fatal("retry changed resource names")
	}
	local, err := NewPlan(p.ClientName, "123456-abcdef.apps.googleusercontent.com", p.ProjectID, "https://local.example/api/hooks/gmail/events", "", "")
	if err != nil || p.Topic != local.Topic || p.Subscription == local.Subscription || p.PushEmail == local.PushEmail {
		t.Fatalf("deployment fanout: %+v %v", local, err)
	}
	for _, endpoint := range []string{"http://localhost/api/hooks/gmail/events", "https://example.com/wrong", "https://user:secret@example.com/api/hooks/gmail/events", "https://example.com/api/hooks/gmail/events?token=secret"} {
		if _, err := NewPlan(p.ClientName, "123456-abcdef.apps.googleusercontent.com", p.ProjectID, endpoint, "", ""); err == nil {
			t.Fatalf("unsafe URL %s", endpoint)
		}
	}
	if _, err := NewPlan(p.ClientName, "123456-abcdef.apps.googleusercontent.com", p.ProjectID, p.Endpoint, "projects/other-project/topics/mail", ""); err == nil {
		t.Fatal("replaced existing topic")
	}
}
