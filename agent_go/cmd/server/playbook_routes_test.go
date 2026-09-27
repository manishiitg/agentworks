package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlaybookReinstallPreservesProgressAndUpdateArchivesEvidence(t *testing.T) {
	item, err := findPlaybook("website-growth-loop")
	if err != nil {
		t.Fatal(err)
	}
	mock := &mockWorkspaceAPI{files: map[string]string{}}
	ws := httptest.NewServer(mock)
	defer ws.Close()
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	ctx := context.Background()
	const workspace = "Workflow/growth"
	const skill = "agentworks-playbook-website-growth-loop"
	const setupPath = workspace + "/skills/" + skill + "/SETUP.json"
	originalHash, err := installPlaybookSkill(ctx, workspace, skill, item)
	if err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{"scripts/validate_growth_artifact.py", "examples/content-brief.json", "examples/invalid-content-brief.json", "examples/reviewable-page-draft.json", "examples/invalid-page-draft.json"} {
		if mock.files[workspace+"/skills/"+skill+"/"+relative] == "" {
			t.Fatalf("Website Growth installation lacks %s", relative)
		}
	}
	var progress map[string]interface{}
	if err := json.Unmarshal([]byte(mock.files[setupPath]), &progress); err != nil {
		t.Fatal(err)
	}
	progress["completed_steps"] = []string{"goal_owner"}
	progress["evidence"] = map[string]string{"goal_owner": "Owner reviewed the actual website and goal"}
	saved, _ := json.Marshal(progress)
	mock.files[setupPath] = string(saved)
	retryHash, err := installPlaybookSkill(ctx, workspace, skill, item)
	if err != nil {
		t.Fatal(err)
	}
	if retryHash != originalHash || mock.files[setupPath] != string(saved) {
		t.Fatal("reinstall changed the source hash or erased customer setup progress")
	}
	// Emulate an older installed version; update must preserve the evidence
	// before replacing the working checklist with the current source.
	progress["playbook_version"] = "0.2.0"
	saved, _ = json.Marshal(progress)
	mock.files[setupPath] = string(saved)
	if _, err := installPlaybookSkill(ctx, workspace, skill, item); err != nil {
		t.Fatal(err)
	}
	var updated struct {
		Previous  string   `json:"previous_setup_path"`
		Completed []string `json:"completed_steps"`
	}
	if err := json.Unmarshal([]byte(mock.files[setupPath]), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Previous == "" || mock.files[updated.Previous] != string(saved) || len(updated.Completed) != 0 {
		t.Fatal("update lost earlier evidence or carried unreviewed checks into the new version")
	}
}

func TestSEOIntelligenceInstallationCopiesTeamContractAndPendingSetup(t *testing.T) {
	item, err := findPlaybook("seo-intelligence")
	if err != nil {
		t.Fatal(err)
	}
	mock := &mockWorkspaceAPI{files: map[string]string{}}
	ws := httptest.NewServer(mock)
	defer ws.Close()
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	const workspace = "Workflow/seo"
	const skill = "agentworks-playbook-seo-intelligence"
	if _, err := installPlaybookSkill(context.Background(), workspace, skill, item); err != nil {
		t.Fatal(err)
	}
	base := workspace + "/skills/" + skill + "/"
	for _, relative := range []string{"SKILL.md", "SETUP.json", "playbook.json", "references/team-and-handoffs.md", "scripts/validate_handoff.py", "examples/seo-issue-list.json", "examples/seo-opportunity-list.json", "examples/invalid-seo-opportunity-list.json"} {
		if mock.files[base+relative] == "" {
			t.Fatalf("SEO Intelligence installation lacks %s", relative)
		}
	}
	var setup struct {
		PlaybookID string   `json:"playbook_id"`
		Completed  []string `json:"completed_steps"`
		Checks     []struct {
			ID string `json:"id"`
		} `json:"checks"`
	}
	if err := json.Unmarshal([]byte(mock.files[base+"SETUP.json"]), &setup); err != nil {
		t.Fatal(err)
	}
	if setup.PlaybookID != item.ID || len(setup.Completed) != 0 || len(setup.Checks) != 10 {
		t.Fatalf("SEO Intelligence setup is not pending with ten checks: %+v", setup)
	}
}

func TestFinOpsInstallationCopiesThreeCrewContractAndPendingSetup(t *testing.T) {
	item, err := findPlaybook("cost-anomaly-to-verified-savings")
	if err != nil {
		t.Fatal(err)
	}
	if len(item.AgentSlots) != 3 || len(item.Handoffs) != 2 || len(item.SetupChecks) != 10 {
		t.Fatalf("FinOps team contract = %+v", item)
	}
	mock := &mockWorkspaceAPI{files: map[string]string{}}
	ws := httptest.NewServer(mock)
	defer ws.Close()
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	const workspace = "Workflow/finops"
	const skill = "agentworks-playbook-cost-anomaly-to-verified-savings"
	if _, err := installPlaybookSkill(context.Background(), workspace, skill, item); err != nil {
		t.Fatal(err)
	}
	base := workspace + "/skills/" + skill + "/"
	for _, relative := range []string{"SKILL.md", "SETUP.json", "playbook.json", "references/team-and-handoffs.md", "scripts/validate_handoff.py", "examples/cloud-cost-review.json", "examples/cloud-change-review.json", "examples/cloud-change-deployed.json", "examples/cloud-savings-pending.json", "examples/cloud-savings-verified.json", "examples/invalid-cloud-savings-verified.json"} {
		if mock.files[base+relative] == "" {
			t.Fatalf("FinOps installation lacks %s", relative)
		}
	}
	var setup struct {
		PlaybookID string   `json:"playbook_id"`
		Completed  []string `json:"completed_steps"`
		Checks     []struct {
			ID string `json:"id"`
		} `json:"checks"`
	}
	if err := json.Unmarshal([]byte(mock.files[base+"SETUP.json"]), &setup); err != nil {
		t.Fatal(err)
	}
	if setup.PlaybookID != item.ID || len(setup.Completed) != 0 || len(setup.Checks) != 10 {
		t.Fatalf("FinOps setup is not pending with ten checks: %+v", setup)
	}
}

func TestAIVisibilityInstallationCopiesSampleContractAndPendingSetup(t *testing.T) {
	item, err := findPlaybook("ai-visibility-intelligence")
	if err != nil {
		t.Fatal(err)
	}
	if len(item.AgentSlots) != 2 || len(item.Handoffs) != 1 || len(item.SetupChecks) != 10 {
		t.Fatalf("AI visibility team contract = %+v", item)
	}
	mock := &mockWorkspaceAPI{files: map[string]string{}}
	ws := httptest.NewServer(mock)
	defer ws.Close()
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	const workspace = "Workflow/ai-visibility"
	const skill = "agentworks-playbook-ai-visibility-intelligence"
	if _, err := installPlaybookSkill(context.Background(), workspace, skill, item); err != nil {
		t.Fatal(err)
	}
	base := workspace + "/skills/" + skill + "/"
	for _, relative := range []string{"SKILL.md", "SETUP.json", "playbook.json", "references/team-and-handoffs.md", "scripts/validate_handoff.py", "examples/ai-visibility-snapshot.json", "examples/ai-citation-opportunity.json", "examples/invalid-ai-citation-opportunity.json"} {
		if mock.files[base+relative] == "" {
			t.Fatalf("AI visibility installation lacks %s", relative)
		}
	}
	var setup struct {
		PlaybookID string   `json:"playbook_id"`
		Completed  []string `json:"completed_steps"`
		Checks     []struct {
			ID string `json:"id"`
		} `json:"checks"`
	}
	if err := json.Unmarshal([]byte(mock.files[base+"SETUP.json"]), &setup); err != nil {
		t.Fatal(err)
	}
	if setup.PlaybookID != item.ID || len(setup.Completed) != 0 || len(setup.Checks) != 10 {
		t.Fatalf("AI visibility setup is not pending with ten checks: %+v", setup)
	}
}

func TestFunnelInstallationCopiesCohortContractAndPendingSetup(t *testing.T) {
	item, err := findPlaybook("funnel-conversion-intelligence")
	if err != nil {
		t.Fatal(err)
	}
	if len(item.AgentSlots) != 2 || len(item.Handoffs) != 1 || len(item.SetupChecks) != 10 {
		t.Fatalf("funnel team contract = %+v", item)
	}
	mock := &mockWorkspaceAPI{files: map[string]string{}}
	ws := httptest.NewServer(mock)
	defer ws.Close()
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	const workspace = "Workflow/funnel"
	const skill = "agentworks-playbook-funnel-conversion-intelligence"
	if _, err := installPlaybookSkill(context.Background(), workspace, skill, item); err != nil {
		t.Fatal(err)
	}
	base := workspace + "/skills/" + skill + "/"
	for _, relative := range []string{"SKILL.md", "SETUP.json", "playbook.json", "references/team-and-handoffs.md", "scripts/validate_handoff.py", "examples/funnel-observation.json", "examples/funnel-baseline-first.json", "examples/funnel-baseline-first-plan.json", "examples/funnel-experiment-plan.json", "examples/invalid-funnel-experiment-plan.json"} {
		if mock.files[base+relative] == "" {
			t.Fatalf("funnel installation lacks %s", relative)
		}
	}
	var setup struct {
		PlaybookID string   `json:"playbook_id"`
		Completed  []string `json:"completed_steps"`
		Checks     []struct {
			ID string `json:"id"`
		} `json:"checks"`
	}
	if err := json.Unmarshal([]byte(mock.files[base+"SETUP.json"]), &setup); err != nil {
		t.Fatal(err)
	}
	if setup.PlaybookID != item.ID || len(setup.Completed) != 0 || len(setup.Checks) != 10 {
		t.Fatalf("funnel setup is not pending with ten checks: %+v", setup)
	}
}

func TestRetentionInstallationCopiesMaturityContractAndPendingSetup(t *testing.T) {
	item, err := findPlaybook("activation-retention-intelligence")
	if err != nil {
		t.Fatal(err)
	}
	if len(item.AgentSlots) != 2 || len(item.Handoffs) != 1 || len(item.SetupChecks) != 10 {
		t.Fatalf("retention team contract = %+v", item)
	}
	mock := &mockWorkspaceAPI{files: map[string]string{}}
	ws := httptest.NewServer(mock)
	defer ws.Close()
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	const workspace = "Workflow/retention"
	const skill = "agentworks-playbook-activation-retention-intelligence"
	if _, err := installPlaybookSkill(context.Background(), workspace, skill, item); err != nil {
		t.Fatal(err)
	}
	base := workspace + "/skills/" + skill + "/"
	for _, relative := range []string{"SKILL.md", "SETUP.json", "playbook.json", "references/team-and-handoffs.md", "scripts/validate_handoff.py", "examples/cohort-retention-observation.json", "examples/cohort-retention-baseline-first.json", "examples/cohort-retention-pending-maturity.json", "examples/retention-experiment-plan.json", "examples/retention-baseline-first-plan.json", "examples/invalid-retention-experiment-plan.json"} {
		if mock.files[base+relative] == "" {
			t.Fatalf("retention installation lacks %s", relative)
		}
	}
	var setup struct {
		PlaybookID string   `json:"playbook_id"`
		Completed  []string `json:"completed_steps"`
		Checks     []struct {
			ID string `json:"id"`
		} `json:"checks"`
	}
	if err := json.Unmarshal([]byte(mock.files[base+"SETUP.json"]), &setup); err != nil {
		t.Fatal(err)
	}
	if setup.PlaybookID != item.ID || len(setup.Completed) != 0 || len(setup.Checks) != 10 {
		t.Fatalf("retention setup is not pending with ten checks: %+v", setup)
	}
}

func TestExperimentFollowThroughCopiesReceiptContractAndPendingSetup(t *testing.T) {
	item, err := findPlaybook("growth-experimentation-follow-through")
	if err != nil {
		t.Fatal(err)
	}
	if len(item.AgentSlots) != 2 || len(item.Handoffs) != 1 || len(item.SetupChecks) != 10 {
		t.Fatalf("experiment team contract = %+v", item)
	}
	mock := &mockWorkspaceAPI{files: map[string]string{}}
	ws := httptest.NewServer(mock)
	defer ws.Close()
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	const workspace = "Workflow/experiment"
	const skill = "agentworks-playbook-growth-experimentation-follow-through"
	if _, err := installPlaybookSkill(context.Background(), workspace, skill, item); err != nil {
		t.Fatal(err)
	}
	base := workspace + "/skills/" + skill + "/"
	for _, relative := range []string{"SKILL.md", "SETUP.json", "playbook.json", "references/team-and-handoffs.md", "scripts/validate_handoff.py", "examples/frozen-experiment-plan.json", "examples/experiment-execution-record.json", "examples/experiment-pending-approval.json", "examples/experiment-pending-window.json", "examples/experiment-inconclusive-readout.json", "examples/experiment-measured-readout.json", "examples/invalid-experiment-execution.json", "examples/invalid-experiment-readout.json"} {
		if mock.files[base+relative] == "" {
			t.Fatalf("experiment installation lacks %s", relative)
		}
	}
	var setup struct {
		PlaybookID string   `json:"playbook_id"`
		Completed  []string `json:"completed_steps"`
		Checks     []struct {
			ID string `json:"id"`
		} `json:"checks"`
	}
	if err := json.Unmarshal([]byte(mock.files[base+"SETUP.json"]), &setup); err != nil {
		t.Fatal(err)
	}
	if setup.PlaybookID != item.ID || len(setup.Completed) != 0 || len(setup.Checks) != 10 {
		t.Fatalf("experiment setup is not pending with ten checks: %+v", setup)
	}
}

func TestPostIncidentInstallationCopiesReviewContractAndPendingSetup(t *testing.T) {
	item, err := findPlaybook("post-incident-review-actions")
	if err != nil {
		t.Fatal(err)
	}
	if len(item.AgentSlots) != 2 || len(item.Handoffs) != 1 || len(item.SetupChecks) != 10 {
		t.Fatalf("post-incident team contract = %+v", item)
	}
	mock := &mockWorkspaceAPI{files: map[string]string{}}
	ws := httptest.NewServer(mock)
	defer ws.Close()
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	const workspace = "Workflow/post-incident"
	const skill = "agentworks-playbook-post-incident-review-actions"
	if _, err := installPlaybookSkill(context.Background(), workspace, skill, item); err != nil {
		t.Fatal(err)
	}
	base := workspace + "/skills/" + skill + "/"
	for _, relative := range []string{"SKILL.md", "SETUP.json", "playbook.json", "references/team-and-handoffs.md", "scripts/validate_handoff.py", "examples/post-incident-review.json", "examples/post-incident-draft.json", "examples/incident-improvement-register.json", "examples/incident-improvement-pending.json", "examples/invalid-post-incident-review.json", "examples/invalid-improvement-register.json"} {
		if mock.files[base+relative] == "" {
			t.Fatalf("post-incident installation lacks %s", relative)
		}
	}
	var setup struct {
		PlaybookID string   `json:"playbook_id"`
		Completed  []string `json:"completed_steps"`
		Checks     []struct {
			ID string `json:"id"`
		} `json:"checks"`
	}
	if err := json.Unmarshal([]byte(mock.files[base+"SETUP.json"]), &setup); err != nil {
		t.Fatal(err)
	}
	if setup.PlaybookID != item.ID || len(setup.Completed) != 0 || len(setup.Checks) != 10 {
		t.Fatalf("post-incident setup is not pending with ten checks: %+v", setup)
	}
}

func TestWorkflowGuideInstallationDoesNotClaimCrewSetup(t *testing.T) {
	item, err := findPlaybook("basic-browser-setup")
	if err != nil {
		t.Fatal(err)
	}
	mock := &mockWorkspaceAPI{files: map[string]string{}}
	ws := httptest.NewServer(mock)
	defer ws.Close()
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	const base = "Workflow/browser/skills/agentworks-playbook-basic-browser-setup/"
	if _, err := installPlaybookSkill(context.Background(), "Workflow/browser", "agentworks-playbook-basic-browser-setup", item); err != nil {
		t.Fatal(err)
	}
	if mock.files[base+"SKILL.md"] == "" || mock.files[base+"playbook.json"] == "" {
		t.Fatal("Workflow guide was not installed")
	}
	if _, hasSetup := mock.files[base+"SETUP.json"]; hasSetup {
		t.Fatal("Workflow guide unexpectedly has a tracked Crew setup checklist")
	}
}

func TestFinancePlaybookInstallationCopiesContractAndPendingSetup(t *testing.T) {
	item, err := findPlaybook("finance-operations-review")
	if err != nil {
		t.Fatal(err)
	}
	mock := &mockWorkspaceAPI{files: map[string]string{}}
	ws := httptest.NewServer(mock)
	defer ws.Close()
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	const workspace = "Workflow/finance"
	const skill = "agentworks-playbook-finance-operations-review"
	if _, err := installPlaybookSkill(context.Background(), workspace, skill, item); err != nil {
		t.Fatal(err)
	}
	base := workspace + "/skills/" + skill + "/"
	for _, relative := range []string{"SKILL.md", "SETUP.json", "playbook.json", "references/team-and-handoffs.md", "scripts/validate_finance_artifact.py", "examples/billing-exception-queue.json"} {
		if mock.files[base+relative] == "" {
			t.Fatalf("finance playbook installation lacks %s", relative)
		}
	}
	var setup struct {
		PlaybookID string   `json:"playbook_id"`
		Completed  []string `json:"completed_steps"`
	}
	if err := json.Unmarshal([]byte(mock.files[base+"SETUP.json"]), &setup); err != nil {
		t.Fatal(err)
	}
	if setup.PlaybookID != item.ID || len(setup.Completed) != 0 {
		t.Fatalf("finance setup was pre-completed: %+v", setup)
	}
}

func TestInvoiceIntakePlaybookInstallationCopiesContractAndPendingSetup(t *testing.T) {
	item, err := findPlaybook("invoice-intake-to-reviewed-payable")
	if err != nil {
		t.Fatal(err)
	}
	mock := &mockWorkspaceAPI{files: map[string]string{}}
	ws := httptest.NewServer(mock)
	defer ws.Close()
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	const workspace = "Workflow/invoice-intake"
	const skill = "agentworks-playbook-invoice-intake-to-reviewed-payable"
	if _, err := installPlaybookSkill(context.Background(), workspace, skill, item); err != nil {
		t.Fatal(err)
	}
	base := workspace + "/skills/" + skill + "/"
	for _, relative := range []string{"SKILL.md", "SETUP.json", "playbook.json", "references/team-and-handoffs.md", "scripts/validate_handoff.py", "examples/document-intake-record.json", "examples/payable-review.json", "examples/invalid-payable-review.json"} {
		if mock.files[base+relative] == "" {
			t.Fatalf("invoice intake playbook installation lacks %s", relative)
		}
	}
	var setup struct {
		PlaybookID string   `json:"playbook_id"`
		Completed  []string `json:"completed_steps"`
	}
	if err := json.Unmarshal([]byte(mock.files[base+"SETUP.json"]), &setup); err != nil {
		t.Fatal(err)
	}
	if setup.PlaybookID != item.ID || len(setup.Completed) != 0 {
		t.Fatalf("invoice intake setup was pre-completed: %+v", setup)
	}
}

func TestMarketingPlaybookInstallationCopiesContractAndPendingSetup(t *testing.T) {
	item, err := findPlaybook("campaign-signal-to-reviewed-experiment")
	if err != nil {
		t.Fatal(err)
	}
	mock := &mockWorkspaceAPI{files: map[string]string{}}
	ws := httptest.NewServer(mock)
	defer ws.Close()
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	const workspace = "Workflow/marketing"
	const skill = "agentworks-playbook-campaign-signal-to-reviewed-experiment"
	if _, err := installPlaybookSkill(context.Background(), workspace, skill, item); err != nil {
		t.Fatal(err)
	}
	base := workspace + "/skills/" + skill + "/"
	for _, relative := range []string{"SKILL.md", "SETUP.json", "playbook.json", "references/team-and-handoffs.md", "scripts/validate_handoff.py", "examples/campaign-performance-brief.json", "examples/competitor-context.json", "examples/growth-experiment-plan.json", "examples/invalid-growth-experiment-plan.json"} {
		if mock.files[base+relative] == "" {
			t.Fatalf("marketing playbook installation lacks %s", relative)
		}
	}
	var setup struct {
		PlaybookID string   `json:"playbook_id"`
		Completed  []string `json:"completed_steps"`
	}
	if err := json.Unmarshal([]byte(mock.files[base+"SETUP.json"]), &setup); err != nil {
		t.Fatal(err)
	}
	if setup.PlaybookID != item.ID || len(setup.Completed) != 0 {
		t.Fatalf("marketing setup was pre-completed: %+v", setup)
	}
}

func TestSalesPlaybookInstallationCopiesContractAndPendingSetup(t *testing.T) {
	item, err := findPlaybook("inbound-lead-to-meeting-review")
	if err != nil {
		t.Fatal(err)
	}
	mock := &mockWorkspaceAPI{files: map[string]string{}}
	ws := httptest.NewServer(mock)
	defer ws.Close()
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	const workspace = "Workflow/sales"
	const skill = "agentworks-playbook-inbound-lead-to-meeting-review"
	if _, err := installPlaybookSkill(context.Background(), workspace, skill, item); err != nil {
		t.Fatal(err)
	}
	base := workspace + "/skills/" + skill + "/"
	for _, relative := range []string{"SKILL.md", "SETUP.json", "playbook.json", "references/team-and-handoffs.md", "references/booking-and-delivery.md", "scripts/validate_sales_artifact.py", "examples/lead-qualification-brief.json", "examples/account-research-brief.json", "examples/sales-followup-draft.json", "examples/sales-delivery-receipt.json", "examples/sales-meeting-outcome.json", "examples/sales-instant-meeting-outcome.json"} {
		if mock.files[base+relative] == "" {
			t.Fatalf("sales playbook installation lacks %s", relative)
		}
	}
	var setup struct {
		PlaybookID string   `json:"playbook_id"`
		Completed  []string `json:"completed_steps"`
	}
	if err := json.Unmarshal([]byte(mock.files[base+"SETUP.json"]), &setup); err != nil {
		t.Fatal(err)
	}
	if setup.PlaybookID != item.ID || len(setup.Completed) != 0 {
		t.Fatalf("sales setup was pre-completed: %+v", setup)
	}
}

func TestCustomerSuccessPlaybookInstallationCopiesContractAndPendingSetup(t *testing.T) {
	item, err := findPlaybook("new-customer-to-first-value")
	if err != nil {
		t.Fatal(err)
	}
	mock := &mockWorkspaceAPI{files: map[string]string{}}
	ws := httptest.NewServer(mock)
	defer ws.Close()
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	const workspace = "Workflow/customer-success"
	const skill = "agentworks-playbook-new-customer-to-first-value"
	if _, err := installPlaybookSkill(context.Background(), workspace, skill, item); err != nil {
		t.Fatal(err)
	}
	base := workspace + "/skills/" + skill + "/"
	for _, relative := range []string{"SKILL.md", "SETUP.json", "playbook.json", "references/team-and-handoffs.md", "scripts/validate_customer_success_artifact.py", "examples/onboarding-milestone-register.json", "examples/first-value-readout.json", "examples/customer-health-brief.json"} {
		if mock.files[base+relative] == "" {
			t.Fatalf("Customer Success playbook installation lacks %s", relative)
		}
	}
	var setup struct {
		PlaybookID string   `json:"playbook_id"`
		Completed  []string `json:"completed_steps"`
	}
	if err := json.Unmarshal([]byte(mock.files[base+"SETUP.json"]), &setup); err != nil {
		t.Fatal(err)
	}
	if setup.PlaybookID != item.ID || len(setup.Completed) != 0 {
		t.Fatalf("Customer Success setup was pre-completed: %+v", setup)
	}
}

func TestSignedDealHandoffInstallationCopiesContractAndPendingSetup(t *testing.T) {
	item, err := findPlaybook("signed-deal-to-onboarding-handoff")
	if err != nil {
		t.Fatal(err)
	}
	if len(item.AgentSlots) != 2 || len(item.Handoffs) != 1 || len(item.SetupChecks) != 10 {
		t.Fatalf("signed deal team contract = %+v", item)
	}
	mock := &mockWorkspaceAPI{files: map[string]string{}}
	ws := httptest.NewServer(mock)
	defer ws.Close()
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	const workspace = "Workflow/signed-deal"
	const skill = "agentworks-playbook-signed-deal-to-onboarding-handoff"
	if _, err := installPlaybookSkill(context.Background(), workspace, skill, item); err != nil {
		t.Fatal(err)
	}
	base := workspace + "/skills/" + skill + "/"
	for _, relative := range []string{"SKILL.md", "SETUP.json", "playbook.json", "references/team-and-handoffs.md", "scripts/validate_handoff.py", "examples/sales-cs-handoff.json", "examples/onboarding-acceptance.json", "examples/onboarding-register-from-accepted.json", "examples/needs-resolution.json", "examples/invalid-onboarding-acceptance.json"} {
		if mock.files[base+relative] == "" {
			t.Fatalf("signed deal installation lacks %s", relative)
		}
	}
	var setup struct {
		PlaybookID string   `json:"playbook_id"`
		Completed  []string `json:"completed_steps"`
		Checks     []struct {
			ID string `json:"id"`
		} `json:"checks"`
	}
	if err := json.Unmarshal([]byte(mock.files[base+"SETUP.json"]), &setup); err != nil {
		t.Fatal(err)
	}
	if setup.PlaybookID != item.ID || len(setup.Completed) != 0 || len(setup.Checks) != 10 {
		t.Fatalf("signed deal setup is not pending with ten checks: %+v", setup)
	}
}

func TestRenewalPlaybookInstallationCopiesContractAndPendingSetup(t *testing.T) {
	item, err := findPlaybook("renewal-risk-to-owned-decision")
	if err != nil {
		t.Fatal(err)
	}
	if len(item.AgentSlots) != 2 || len(item.Handoffs) != 1 || len(item.SetupChecks) != 10 {
		t.Fatalf("renewal team contract = %+v", item)
	}
	mock := &mockWorkspaceAPI{files: map[string]string{}}
	ws := httptest.NewServer(mock)
	defer ws.Close()
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	const workspace = "Workflow/renewal"
	const skill = "agentworks-playbook-renewal-risk-to-owned-decision"
	if _, err := installPlaybookSkill(context.Background(), workspace, skill, item); err != nil {
		t.Fatal(err)
	}
	base := workspace + "/skills/" + skill + "/"
	for _, relative := range []string{"SKILL.md", "SETUP.json", "playbook.json", "references/team-and-handoffs.md", "scripts/validate_handoff.py", "examples/renewal-health-brief.json", "examples/renewal-health-unknown.json", "examples/renewal-decision-register.json", "examples/renewal-terms-pending.json", "examples/invalid-renewal-decision.json"} {
		if mock.files[base+relative] == "" {
			t.Fatalf("renewal installation lacks %s", relative)
		}
	}
	var setup struct {
		PlaybookID string   `json:"playbook_id"`
		Completed  []string `json:"completed_steps"`
		Checks     []struct {
			ID string `json:"id"`
		} `json:"checks"`
	}
	if err := json.Unmarshal([]byte(mock.files[base+"SETUP.json"]), &setup); err != nil {
		t.Fatal(err)
	}
	if setup.PlaybookID != item.ID || len(setup.Completed) != 0 || len(setup.Checks) != 10 {
		t.Fatalf("renewal setup is not pending with ten checks: %+v", setup)
	}
}

func TestFeatureAdoptionPlaybookInstallationCopiesContractAndPendingSetup(t *testing.T) {
	item, err := findPlaybook("released-feature-to-adoption-decision")
	if err != nil {
		t.Fatal(err)
	}
	if len(item.AgentSlots) != 2 || len(item.Handoffs) != 1 || len(item.SetupChecks) != 10 {
		t.Fatalf("feature adoption team contract = %+v", item)
	}
	mock := &mockWorkspaceAPI{files: map[string]string{}}
	ws := httptest.NewServer(mock)
	defer ws.Close()
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	const workspace = "Workflow/product"
	const skill = "agentworks-playbook-released-feature-to-adoption-decision"
	if _, err := installPlaybookSkill(context.Background(), workspace, skill, item); err != nil {
		t.Fatal(err)
	}
	base := workspace + "/skills/" + skill + "/"
	for _, relative := range []string{"SKILL.md", "SETUP.json", "playbook.json", "references/team-and-handoffs.md", "scripts/validate_handoff.py", "examples/feature-adoption-observation.json", "examples/feature-adoption-comparable.json", "examples/feature-adoption-not-evaluable.json", "examples/feature-adoption-decision.json", "examples/invalid-feature-adoption-decision.json"} {
		if mock.files[base+relative] == "" {
			t.Fatalf("feature adoption installation lacks %s", relative)
		}
	}
	var setup struct {
		PlaybookID string   `json:"playbook_id"`
		Completed  []string `json:"completed_steps"`
		Checks     []struct {
			ID string `json:"id"`
		} `json:"checks"`
	}
	if err := json.Unmarshal([]byte(mock.files[base+"SETUP.json"]), &setup); err != nil {
		t.Fatal(err)
	}
	if setup.PlaybookID != item.ID || len(setup.Completed) != 0 || len(setup.Checks) != 10 {
		t.Fatalf("feature adoption setup is not pending with ten checks: %+v", setup)
	}
}

func TestVendorPurchasePlaybookInstallationCopiesContractAndPendingSetup(t *testing.T) {
	item, err := findPlaybook("vendor-evaluation-to-purchase-decision")
	if err != nil {
		t.Fatal(err)
	}
	if len(item.AgentSlots) != 2 || len(item.Handoffs) != 1 || len(item.SetupChecks) != 10 {
		t.Fatalf("vendor purchase team contract = %+v", item)
	}
	mock := &mockWorkspaceAPI{files: map[string]string{}}
	ws := httptest.NewServer(mock)
	defer ws.Close()
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	const workspace = "Workflow/vendor-purchase"
	const skill = "agentworks-playbook-vendor-evaluation-to-purchase-decision"
	if _, err := installPlaybookSkill(context.Background(), workspace, skill, item); err != nil {
		t.Fatal(err)
	}
	base := workspace + "/skills/" + skill + "/"
	for _, relative := range []string{"SKILL.md", "SETUP.json", "playbook.json", "references/team-and-handoffs.md", "scripts/validate_handoff.py", "examples/vendor-comparison.json", "examples/vendor-purchase-pending.json", "examples/vendor-purchase-approved.json", "examples/vendor-purchase-blocked.json", "examples/invalid-vendor-purchase-review.json"} {
		if mock.files[base+relative] == "" {
			t.Fatalf("vendor purchase installation lacks %s", relative)
		}
	}
	var setup struct {
		PlaybookID string   `json:"playbook_id"`
		Completed  []string `json:"completed_steps"`
		Checks     []struct {
			ID string `json:"id"`
		} `json:"checks"`
	}
	if err := json.Unmarshal([]byte(mock.files[base+"SETUP.json"]), &setup); err != nil {
		t.Fatal(err)
	}
	if setup.PlaybookID != item.ID || len(setup.Completed) != 0 || len(setup.Checks) != 10 {
		t.Fatalf("vendor purchase setup is not pending with ten checks: %+v", setup)
	}
}

func TestEngineeringOperationsIntelligenceInstallsCrewContractAndPendingSetup(t *testing.T) {
	item, err := findPlaybook("engineering-operations-intelligence")
	if err != nil {
		t.Fatal(err)
	}
	if len(item.AgentSlots) != 2 || len(item.Handoffs) != 1 || len(item.SetupChecks) != 10 {
		t.Fatalf("engineering operations team contract = %+v", item)
	}
	mock := &mockWorkspaceAPI{files: map[string]string{}}
	ws := httptest.NewServer(mock)
	defer ws.Close()
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	const workspace = "Workflow/engineering-operations"
	const skill = "agentworks-playbook-engineering-operations-intelligence"
	if _, err := installPlaybookSkill(context.Background(), workspace, skill, item); err != nil {
		t.Fatal(err)
	}
	base := workspace + "/skills/" + skill + "/"
	for _, relative := range []string{"SKILL.md", "SETUP.json", "playbook.json", "references/team-and-handoffs.md", "references/metrics-and-findings.md", "scripts/validate_handoff.py", "examples/engineering-metric-observation.json", "examples/engineering-metric-baseline.json", "examples/engineering-metric-not-evaluable.json", "examples/engineering-improvement-review.json", "examples/invalid-engineering-improvement-review.json"} {
		if mock.files[base+relative] == "" {
			t.Fatalf("engineering operations installation lacks %s", relative)
		}
	}
	var setup struct {
		PlaybookID string   `json:"playbook_id"`
		Completed  []string `json:"completed_steps"`
		Checks     []struct {
			ID string `json:"id"`
		} `json:"checks"`
	}
	if err := json.Unmarshal([]byte(mock.files[base+"SETUP.json"]), &setup); err != nil {
		t.Fatal(err)
	}
	if setup.PlaybookID != item.ID || len(setup.Completed) != 0 || len(setup.Checks) != 10 {
		t.Fatalf("engineering operations setup is not pending with ten checks: %+v", setup)
	}
}

func TestEngineeringPlaybookInstallationCopiesContractAndPendingSetup(t *testing.T) {
	item, err := findPlaybook("incident-to-verified-recovery")
	if err != nil {
		t.Fatal(err)
	}
	mock := &mockWorkspaceAPI{files: map[string]string{}}
	ws := httptest.NewServer(mock)
	defer ws.Close()
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	const workspace = "Workflow/engineering"
	const skill = "agentworks-playbook-incident-to-verified-recovery"
	if _, err := installPlaybookSkill(context.Background(), workspace, skill, item); err != nil {
		t.Fatal(err)
	}
	base := workspace + "/skills/" + skill + "/"
	for _, relative := range []string{"SKILL.md", "SETUP.json", "playbook.json", "references/team-and-handoffs.md", "scripts/validate_handoff.py", "examples/incident-investigation.json", "examples/engineering-blocker-ledger.json", "examples/invalid-engineering-blocker-ledger.json"} {
		if mock.files[base+relative] == "" {
			t.Fatalf("Engineering playbook installation lacks %s", relative)
		}
	}
	var setup struct {
		PlaybookID string   `json:"playbook_id"`
		Completed  []string `json:"completed_steps"`
	}
	if err := json.Unmarshal([]byte(mock.files[base+"SETUP.json"]), &setup); err != nil {
		t.Fatal(err)
	}
	if setup.PlaybookID != item.ID || len(setup.Completed) != 0 {
		t.Fatalf("Engineering setup was pre-completed: %+v", setup)
	}
}

func TestGTMPlaybookInstallationCopiesContractAndPendingSetup(t *testing.T) {
	item, err := findPlaybook("launch-to-qualified-pipeline")
	if err != nil {
		t.Fatal(err)
	}
	mock := &mockWorkspaceAPI{files: map[string]string{}}
	ws := httptest.NewServer(mock)
	defer ws.Close()
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	const workspace = "Workflow/gtm"
	const skill = "agentworks-playbook-launch-to-qualified-pipeline"
	if _, err := installPlaybookSkill(context.Background(), workspace, skill, item); err != nil {
		t.Fatal(err)
	}
	base := workspace + "/skills/" + skill + "/"
	for _, relative := range []string{
		"SKILL.md", "SETUP.json", "playbook.json", "references/team-and-handoffs.md",
		"scripts/validate_handoff.py", "examples/gtm-launch-brief.json",
		"examples/launch-signal-register.json", "examples/lead-qualification-brief.json",
	} {
		if mock.files[base+relative] == "" {
			t.Fatalf("GTM playbook installation lacks %s", relative)
		}
	}
	var setup map[string]interface{}
	if err := json.Unmarshal([]byte(mock.files[base+"SETUP.json"]), &setup); err != nil {
		t.Fatal(err)
	}
	completed, ok := setup["completed_steps"].([]interface{})
	if setup["playbook_id"] != item.ID || !ok || len(completed) != 0 {
		t.Fatalf("GTM setup was pre-completed: %+v", setup)
	}
}

func TestCustomerSupportPlaybookInstallationCopiesContractAndPendingSetup(t *testing.T) {
	item, err := findPlaybook("support-case-to-reviewed-resolution")
	if err != nil {
		t.Fatal(err)
	}
	mock := &mockWorkspaceAPI{files: map[string]string{}}
	ws := httptest.NewServer(mock)
	defer ws.Close()
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	const workspace = "Workflow/support"
	const skill = "agentworks-playbook-support-case-to-reviewed-resolution"
	if _, err := installPlaybookSkill(context.Background(), workspace, skill, item); err != nil {
		t.Fatal(err)
	}
	base := workspace + "/skills/" + skill + "/"
	for _, relative := range []string{
		"SKILL.md", "SETUP.json", "playbook.json", "references/team-and-handoffs.md",
		"scripts/validate_handoff.py", "examples/support-case-triage.json",
		"examples/support-reply-draft.json", "examples/approved-support-reply.json", "examples/support-escalation-brief.json",
		"examples/provider-delivery.json", "examples/case-outcome.json",
	} {
		if mock.files[base+relative] == "" {
			t.Fatalf("Customer Support playbook installation lacks %s", relative)
		}
	}
	var setup map[string]interface{}
	if err := json.Unmarshal([]byte(mock.files[base+"SETUP.json"]), &setup); err != nil {
		t.Fatal(err)
	}
	completed, ok := setup["completed_steps"].([]interface{})
	checks, checksOK := setup["checks"].([]interface{})
	if setup["playbook_id"] != item.ID || !ok || len(completed) != 0 || !checksOK || len(checks) != 10 {
		t.Fatalf("Customer Support setup invalid: %+v", setup)
	}
}

func TestCategoryRoutePlaybooksInstallSelfContainedContracts(t *testing.T) {
	for _, id := range []string{
		"opportunity-to-reviewed-requirements",
		"approved-requirement-to-release-readiness",
		"offer-to-seller-readiness",
		"launch-signal-to-pipeline-review",
		"case-pattern-to-knowledge-review",
	} {
		t.Run(id, func(t *testing.T) {
			item, err := findPlaybook(id)
			if err != nil {
				t.Fatal(err)
			}
			if len(item.AgentSlots) < 2 || len(item.Handoffs) < 1 || len(item.SetupChecks) != 10 {
				t.Fatalf("incomplete Crew route contract: %+v", item)
			}
			mock := &mockWorkspaceAPI{files: map[string]string{}}
			ws := httptest.NewServer(mock)
			defer ws.Close()
			t.Setenv("WORKSPACE_API_URL", ws.URL)
			const workspace = "Workflow/category-route"
			skill := "agentworks-playbook-" + id
			if _, err := installPlaybookSkill(context.Background(), workspace, skill, item); err != nil {
				t.Fatal(err)
			}
			base := workspace + "/skills/" + skill + "/"
			for _, relative := range []string{"SKILL.md", "SETUP.json", "playbook.json", "references/team-and-handoffs.md", "scripts/validate_handoff.py", "scripts/category_route_contract.py"} {
				if mock.files[base+relative] == "" {
					t.Fatalf("installation lacks %s", relative)
				}
			}
			var setup struct {
				PlaybookID string   `json:"playbook_id"`
				Completed  []string `json:"completed_steps"`
				Checks     []struct {
					ID string `json:"id"`
				} `json:"checks"`
			}
			if err := json.Unmarshal([]byte(mock.files[base+"SETUP.json"]), &setup); err != nil {
				t.Fatal(err)
			}
			if setup.PlaybookID != id || len(setup.Completed) != 0 || len(setup.Checks) != 10 {
				t.Fatalf("setup must start pending with ten checks: %+v", setup)
			}
		})
	}
}

func TestQAPlaybookInstallationCopiesContractAndPendingSetup(t *testing.T) {
	item, err := findPlaybook("release-candidate-to-reviewed-gate")
	if err != nil {
		t.Fatal(err)
	}
	mock := &mockWorkspaceAPI{files: map[string]string{}}
	ws := httptest.NewServer(mock)
	defer ws.Close()
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	const workspace = "Workflow/qa"
	const skill = "agentworks-playbook-release-candidate-to-reviewed-gate"
	if _, err := installPlaybookSkill(context.Background(), workspace, skill, item); err != nil {
		t.Fatal(err)
	}
	base := workspace + "/skills/" + skill + "/"
	for _, relative := range []string{
		"SKILL.md", "SETUP.json", "playbook.json", "references/team-and-handoffs.md",
		"scripts/validate_handoff.py", "examples/journey-result.json",
		"examples/flake-investigation.json", "examples/release-quality-brief.json",
		"examples/flake-needs-review-gate.json", "examples/status-publication.json",
	} {
		if mock.files[base+relative] == "" {
			t.Fatalf("QA playbook installation lacks %s", relative)
		}
	}
	var setup map[string]interface{}
	if err := json.Unmarshal([]byte(mock.files[base+"SETUP.json"]), &setup); err != nil {
		t.Fatal(err)
	}
	completed, ok := setup["completed_steps"].([]interface{})
	checks, checksOK := setup["checks"].([]interface{})
	if setup["playbook_id"] != item.ID || !ok || len(completed) != 0 || !checksOK || len(checks) != 10 {
		t.Fatalf("QA setup invalid: %+v", setup)
	}
}

func TestSecurityPlaybookInstallationCopiesContractAndPendingSetup(t *testing.T) {
	item, err := findPlaybook("finding-to-verified-remediation")
	if err != nil {
		t.Fatal(err)
	}
	mock := &mockWorkspaceAPI{files: map[string]string{}}
	ws := httptest.NewServer(mock)
	defer ws.Close()
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	const workspace = "Workflow/security"
	const skill = "agentworks-playbook-finding-to-verified-remediation"
	if _, err := installPlaybookSkill(context.Background(), workspace, skill, item); err != nil {
		t.Fatal(err)
	}
	base := workspace + "/skills/" + skill + "/"
	for _, relative := range []string{
		"SKILL.md", "SETUP.json", "playbook.json", "references/team-and-handoffs.md",
		"scripts/validate_handoff.py", "examples/security-finding.json",
		"examples/security-remediation-ledger.json", "examples/verified-security-remediation-ledger.json",
		"examples/invalid-security-remediation-ledger.json",
	} {
		if mock.files[base+relative] == "" {
			t.Fatalf("Security playbook installation lacks %s", relative)
		}
	}
	var setup map[string]interface{}
	if err := json.Unmarshal([]byte(mock.files[base+"SETUP.json"]), &setup); err != nil {
		t.Fatal(err)
	}
	completed, ok := setup["completed_steps"].([]interface{})
	checks, checksOK := setup["checks"].([]interface{})
	if setup["playbook_id"] != item.ID || !ok || len(completed) != 0 || !checksOK || len(checks) != 10 {
		t.Fatalf("Security setup invalid: %+v", setup)
	}
}

func TestAccessExceptionPlaybookInstallationKeepsValidationAndPendingSetup(t *testing.T) {
	item, err := findPlaybook("access-exception-to-verified-fix")
	if err != nil {
		t.Fatal(err)
	}
	mock := &mockWorkspaceAPI{files: map[string]string{}}
	ws := httptest.NewServer(mock)
	defer ws.Close()
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	const workspace = "Workflow/access-review"
	const skill = "agentworks-playbook-access-exception-to-verified-fix"
	if _, err := installPlaybookSkill(context.Background(), workspace, skill, item); err != nil {
		t.Fatal(err)
	}
	base := workspace + "/skills/" + skill + "/"
	for _, relative := range []string{
		"SKILL.md", "SETUP.json", "playbook.json", "references/team-and-handoffs.md",
		"scripts/validate_handoff.py", "examples/access-review-matrix.json",
		"examples/access-remediation-pending.json", "examples/access-remediation-verified.json",
		"examples/invalid-access-remediation.json",
	} {
		if mock.files[base+relative] == "" {
			t.Fatalf("access exception installation lacks %s", relative)
		}
	}
	var setup map[string]interface{}
	if err := json.Unmarshal([]byte(mock.files[base+"SETUP.json"]), &setup); err != nil {
		t.Fatal(err)
	}
	completed, ok := setup["completed_steps"].([]interface{})
	checks, checksOK := setup["checks"].([]interface{})
	if setup["playbook_id"] != item.ID || !ok || len(completed) != 0 || !checksOK || len(checks) != 10 {
		t.Fatalf("access exception setup invalid: %+v", setup)
	}
}

func TestOperationsPlaybookInstallationCopiesContractAndPendingSetup(t *testing.T) {
	item, err := findPlaybook("meeting-decision-to-owned-follow-through")
	if err != nil {
		t.Fatal(err)
	}
	mock := &mockWorkspaceAPI{files: map[string]string{}}
	ws := httptest.NewServer(mock)
	defer ws.Close()
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	const workspace = "Workflow/operations"
	const skill = "agentworks-playbook-meeting-decision-to-owned-follow-through"
	if _, err := installPlaybookSkill(context.Background(), workspace, skill, item); err != nil {
		t.Fatal(err)
	}
	base := workspace + "/skills/" + skill + "/"
	for _, relative := range []string{
		"SKILL.md", "SETUP.json", "playbook.json", "references/team-and-handoffs.md",
		"scripts/validate_handoff.py", "examples/meeting-action-register.json",
		"examples/project-action-status.json", "examples/operations-review-brief.json",
		"examples/invalid-project-action-status.json",
	} {
		if mock.files[base+relative] == "" {
			t.Fatalf("Operations playbook installation lacks %s", relative)
		}
	}
	var setup map[string]interface{}
	if err := json.Unmarshal([]byte(mock.files[base+"SETUP.json"]), &setup); err != nil {
		t.Fatal(err)
	}
	completed, ok := setup["completed_steps"].([]interface{})
	checks, checksOK := setup["checks"].([]interface{})
	if setup["playbook_id"] != item.ID || !ok || len(completed) != 0 || !checksOK || len(checks) != 10 {
		t.Fatalf("Operations setup invalid: %+v", setup)
	}
}

func TestOrderExceptionPlaybookInstallationCopiesContractAndPendingSetup(t *testing.T) {
	item, err := findPlaybook("order-exception-to-reviewed-update")
	if err != nil {
		t.Fatal(err)
	}
	mock := &mockWorkspaceAPI{files: map[string]string{}}
	ws := httptest.NewServer(mock)
	defer ws.Close()
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	const workspace = "Workflow/order-update"
	const skill = "agentworks-playbook-order-exception-to-reviewed-update"
	if _, err := installPlaybookSkill(context.Background(), workspace, skill, item); err != nil {
		t.Fatal(err)
	}
	base := workspace + "/skills/" + skill + "/"
	for _, relative := range []string{
		"SKILL.md", "SETUP.json", "playbook.json", "references/team-and-handoffs.md",
		"scripts/validate_handoff.py", "scripts/test_validate_handoff.py",
		"examples/order-exception.json", "examples/order-customer-update-review.json",
		"examples/invalid-order-customer-update-review.json",
	} {
		if mock.files[base+relative] == "" {
			t.Fatalf("order update playbook installation lacks %s", relative)
		}
	}
	var setup struct {
		PlaybookID string   `json:"playbook_id"`
		Completed  []string `json:"completed_steps"`
		Checks     []struct {
			ID string `json:"id"`
		} `json:"checks"`
	}
	if err := json.Unmarshal([]byte(mock.files[base+"SETUP.json"]), &setup); err != nil {
		t.Fatal(err)
	}
	if setup.PlaybookID != item.ID || len(setup.Completed) != 0 || len(setup.Checks) != 10 {
		t.Fatalf("order update setup invalid: %+v", setup)
	}
}

func TestShopifyPlaybookInstallationCopiesContractAndPendingSetup(t *testing.T) {
	item, err := findPlaybook("order-exception-to-resolution")
	if err != nil {
		t.Fatal(err)
	}
	mock := &mockWorkspaceAPI{files: map[string]string{}}
	ws := httptest.NewServer(mock)
	defer ws.Close()
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	const workspace = "Workflow/shopify"
	const skill = "agentworks-playbook-order-exception-to-resolution"
	if _, err := installPlaybookSkill(context.Background(), workspace, skill, item); err != nil {
		t.Fatal(err)
	}
	base := workspace + "/skills/" + skill + "/"
	for _, relative := range []string{"SKILL.md", "SETUP.json", "playbook.json", "references/team-and-handoffs.md", "scripts/validate_handoff.py", "examples/store-order-exception.json", "examples/return-resolution-review.json", "examples/invalid-return-resolution-review.json"} {
		if mock.files[base+relative] == "" {
			t.Fatalf("Shopify playbook installation lacks %s", relative)
		}
	}
	var setup struct {
		PlaybookID string   `json:"playbook_id"`
		Completed  []string `json:"completed_steps"`
	}
	if err := json.Unmarshal([]byte(mock.files[base+"SETUP.json"]), &setup); err != nil {
		t.Fatal(err)
	}
	if setup.PlaybookID != item.ID || len(setup.Completed) != 0 {
		t.Fatalf("Shopify setup was pre-completed: %+v", setup)
	}
}

func TestShopifyStorefrontPlaybookInstallationCopiesContractAndPendingSetup(t *testing.T) {
	item, err := findPlaybook("storefront-opportunity-to-verified-change")
	if err != nil {
		t.Fatal(err)
	}
	mock := &mockWorkspaceAPI{files: map[string]string{}}
	ws := httptest.NewServer(mock)
	defer ws.Close()
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	const workspace = "Workflow/shopify-storefront"
	const skill = "agentworks-playbook-storefront-opportunity-to-verified-change"
	if _, err := installPlaybookSkill(context.Background(), workspace, skill, item); err != nil {
		t.Fatal(err)
	}
	base := workspace + "/skills/" + skill + "/"
	for _, relative := range []string{"SKILL.md", "SETUP.json", "playbook.json", "references/team-and-handoffs.md", "scripts/validate_handoff.py", "examples/shopify-growth-opportunity.json", "examples/catalog-change-review.json", "examples/invalid-catalog-change-review.json"} {
		if mock.files[base+relative] == "" {
			t.Fatalf("Shopify storefront playbook installation lacks %s", relative)
		}
	}
	var setup struct {
		PlaybookID string   `json:"playbook_id"`
		Completed  []string `json:"completed_steps"`
	}
	if err := json.Unmarshal([]byte(mock.files[base+"SETUP.json"]), &setup); err != nil {
		t.Fatal(err)
	}
	if setup.PlaybookID != item.ID || len(setup.Completed) != 0 {
		t.Fatalf("Shopify storefront setup was pre-completed: %+v", setup)
	}
}

func TestExpandedShopifyPlaybooksInstallWithPendingSetup(t *testing.T) {
	for _, id := range []string{"inventory-availability-to-owner-action", "payment-exception-to-order-decision", "product-launch-readiness-to-go-no-go"} {
		t.Run(id, func(t *testing.T) {
			item, err := findPlaybook(id)
			if err != nil {
				t.Fatal(err)
			}
			mock := &mockWorkspaceAPI{files: map[string]string{}}
			ws := httptest.NewServer(mock)
			defer ws.Close()
			t.Setenv("WORKSPACE_API_URL", ws.URL)
			const workspace = "Workflow/shopify-library"
			skill := "agentworks-playbook-" + id
			if _, err := installPlaybookSkill(context.Background(), workspace, skill, item); err != nil {
				t.Fatal(err)
			}
			base := workspace + "/skills/" + skill + "/"
			for _, relative := range []string{"SKILL.md", "SETUP.json", "playbook.json", "references/team-and-handoffs.md", "scripts/validate_handoff.py"} {
				if mock.files[base+relative] == "" {
					t.Fatalf("%s lacks %s", id, relative)
				}
			}
			var setup struct {
				PlaybookID string   `json:"playbook_id"`
				Completed  []string `json:"completed_steps"`
			}
			if err := json.Unmarshal([]byte(mock.files[base+"SETUP.json"]), &setup); err != nil {
				t.Fatal(err)
			}
			if setup.PlaybookID != id || len(setup.Completed) != 0 {
				t.Fatalf("%s setup = %+v", id, setup)
			}
		})
	}
}

func TestLoadPlaybookCatalogFindsEngineeringPlaybooks(t *testing.T) {
	items, err := loadPlaybookCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 40 {
		t.Fatalf("catalog has %d playbooks, want 40", len(items))
	}
	shopify, err := findPlaybook("order-exception-to-resolution")
	if err != nil {
		t.Fatal(err)
	}
	if shopify.Category != "Shopify" || len(shopify.AgentSlots) != 2 || len(shopify.Handoffs) != 1 || len(shopify.SetupChecks) != 10 {
		t.Fatalf("order exception to resolution = %+v", shopify)
	}
	storefront, err := findPlaybook("storefront-opportunity-to-verified-change")
	if err != nil {
		t.Fatal(err)
	}
	if storefront.Category != "Shopify" || len(storefront.AgentSlots) != 2 || len(storefront.Handoffs) != 1 || len(storefront.SetupChecks) != 10 {
		t.Fatalf("storefront opportunity to verified change = %+v", storefront)
	}
	for _, id := range []string{"inventory-availability-to-owner-action", "payment-exception-to-order-decision", "product-launch-readiness-to-go-no-go"} {
		item, err := findPlaybook(id)
		if err != nil {
			t.Fatal(err)
		}
		if item.Category != "Shopify" || len(item.AgentSlots) != 2 || len(item.Handoffs) != 1 || len(item.SetupChecks) != 10 {
			t.Fatalf("%s = %+v", id, item)
		}
	}
	engineering, err := findPlaybook("incident-to-verified-recovery")
	if err != nil {
		t.Fatal(err)
	}
	if engineering.Category != "Engineering" || len(engineering.AgentSlots) != 2 || len(engineering.Handoffs) != 1 || len(engineering.SetupChecks) != 10 {
		t.Fatalf("incident to verified recovery = %+v", engineering)
	}
	gtm, err := findPlaybook("launch-to-qualified-pipeline")
	if err != nil {
		t.Fatal(err)
	}
	if gtm.Category != "GTM" || len(gtm.AgentSlots) != 5 || len(gtm.Handoffs) != 3 || len(gtm.SetupChecks) != 10 {
		t.Fatalf("launch to qualified pipeline = %+v", gtm)
	}
	success, err := findPlaybook("new-customer-to-first-value")
	if err != nil {
		t.Fatal(err)
	}
	if success.Category != "Customer Success" || len(success.AgentSlots) != 3 || len(success.SetupChecks) != 10 {
		t.Fatalf("new customer to first value = %+v", success)
	}
	sales, err := findPlaybook("inbound-lead-to-meeting-review")
	if err != nil {
		t.Fatal(err)
	}
	if sales.Category != "Sales" || len(sales.AgentSlots) != 3 || len(sales.SetupChecks) != 10 {
		t.Fatalf("inbound lead review = %+v", sales)
	}
	finance, err := findPlaybook("finance-operations-review")
	if err != nil {
		t.Fatal(err)
	}
	if finance.Category != "Finance" || len(finance.AgentSlots) != 4 || len(finance.SetupChecks) != 10 {
		t.Fatalf("finance operations review = %+v", finance)
	}
	growth, err := findPlaybook("website-growth-loop")
	if err != nil {
		t.Fatal(err)
	}
	if growth.Category != "Website Growth" || growth.Version != "0.3.0" {
		t.Fatalf("website growth loop = %+v", growth)
	}
	setupSource, err := os.ReadFile(filepath.Join(growth.SourceDir, "SETUP.json"))
	if err != nil {
		t.Fatal(err)
	}
	var setup struct {
		PlaybookID      string `json:"playbook_id"`
		PlaybookVersion string `json:"playbook_version"`
		Checks          []struct {
			ID string `json:"id"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(setupSource, &setup); err != nil {
		t.Fatal(err)
	}
	if setup.PlaybookID != growth.ID || setup.PlaybookVersion != growth.Version || len(setup.Checks) != len(growth.SetupChecks) {
		t.Fatalf("website growth setup = %+v", setup)
	}
	intelligence, err := findPlaybook("engineering-operations-intelligence")
	if err != nil {
		t.Fatal(err)
	}
	if intelligence.TeamScope != "small_team" || intelligence.Category != "Engineering Operations Intelligence" {
		t.Fatalf("engineering intelligence = %+v", intelligence)
	}
	item, err := findPlaybook("application-security-assessment-remediation")
	if err != nil {
		t.Fatal(err)
	}
	if item.Category != "Security Engineering" {
		t.Fatalf("category = %q, want Security Engineering", item.Category)
	}
	for _, playbook := range items {
		if len(playbook.PulseFocus) != 1 || playbook.PulseFocus[0]["module"] != pulseModuleStrategicReview {
			t.Fatalf("playbook %s focus = %#v, want strategic only", playbook.ID, playbook.PulseFocus)
		}
	}
}

func TestStrategicPlaybookFocusIgnoresLegacyReviewerSpecialization(t *testing.T) {
	focuses := []map[string]interface{}{
		{"module": pulseModuleTechnicalReview, "label": "legacy technical"},
		{"module": pulseModuleArchitectureReview, "label": "legacy architecture"},
		{"module": pulseModuleStrategicReview, "label": "strategy"},
	}
	filtered := strategicPlaybookFocus(focuses)
	if len(filtered) != 1 || filtered[0]["label"] != "strategy" {
		t.Fatalf("filtered focus = %#v, want strategy only", filtered)
	}
}

func TestRewriteExternalPlaybookLinksMaterializesSharedReferences(t *testing.T) {
	item, err := findPlaybook("basic-browser-setup")
	if err != nil {
		t.Fatal(err)
	}
	skillPath := filepath.Join(item.SourceDir, "SKILL.md")
	content, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatal(err)
	}
	external := map[string]string{}
	rewritten := rewriteExternalPlaybookLinks(string(content), skillPath, item.SourceDir, external)
	if strings.Contains(rewritten, "](../references/") {
		t.Fatalf("shared links were not rewritten:\n%s", rewritten)
	}
	if len(external) != 3 {
		t.Fatalf("materialized references = %v, want three", external)
	}
}

func TestInstalledBuilderSkillNamesExcludesDisabledAndDuplicates(t *testing.T) {
	names := installedBuilderSkillNames([]InstalledPlaybook{
		{SkillName: "basic-browser-setup", Status: "draft"},
		{SkillName: "basic-browser-setup", Status: "ready"},
		{SkillName: "disabled-guide", Status: "disabled"},
		{SkillName: "", Status: "ready"},
	})
	if len(names) != 1 || names[0] != "basic-browser-setup" {
		t.Fatalf("builder skills = %v", names)
	}
}
