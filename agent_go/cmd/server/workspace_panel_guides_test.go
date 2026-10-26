package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/skills"
)

// A guide that points to an unpackaged reference cannot teach either Builder
// transport. The same source package must survive native and MCP delivery.
func TestSharedPanelReferencesReachBothTransports(t *testing.T) {
	for _, name := range skills.WorkspacePanelNames() {
		native := skills.BuiltinDefinition(name)
		references := map[string]string{}
		for _, file := range native.SupportingFiles {
			references[file.RelPath] = string(file.Content)
		}
		for _, match := range regexp.MustCompile("(?:\\]\\(|`)(references/[^)`]+)(?:\\)|`)").FindAllStringSubmatch(native.Content, -1) {
			if references[match[1]] == "" {
				t.Fatalf("%s points to missing %s", name, match[1])
			}
		}
		topic := externalGuidanceTopicByName(name)
		if topic == nil {
			continue
		}
		delivered, err := externalGuidanceSkillFiles(nil, *topic)
		if err != nil {
			t.Fatal(err)
		}
		files := map[string]string{}
		for _, raw := range delivered {
			file := raw.(map[string]any)
			files[file["path"].(string)] = file["content"].(string)
		}
		for path, content := range references {
			if files[path] != builderProductSelection(nil).Text(content) {
				t.Fatalf("%s/%s differs between transports", name, path)
			}
		}
	}
}

func TestWhatsAppRelayRouteUsesCurrentProductAccess(t *testing.T) {
	f := newExternalToolsFixture(t)
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","role":"editor","products":["agentworks","relays"]},{"id":"reader","role":"viewer","products":["agentworks","relays"]},{"id":"outsider","role":"viewer","products":["agentworks","relays"]}]}`)
	const root = "Workflow/invoices" // This fixture serves this canonical manifest path.
	f.write(t, root+"/workflow.json", `{"id":"review-flow","kind":"relay","label":"Review","created_by":"owner","access":{"owners":["owner"],"readers":["reader"]}}`)
	route := services.ChannelRoute{WorkflowID: "review-flow", WorkspacePath: root, WorkshopMode: "run"}
	for _, user := range []string{"owner", "reader"} {
		if ok, err := f.api.checkWhatsAppWorkflowAccess(t.Context(), user, route); err != nil || !ok {
			t.Fatalf("%s Flow access: %t %v", user, ok, err)
		}
	}
	if ok, _ := f.api.checkWhatsAppWorkflowAccess(t.Context(), "outsider", route); ok {
		t.Fatal("unshared caller reached the Flow")
	}
	if _, ok, _ := f.api.checkBotWorkflowAccess(t.Context(), "owner", "", route); ok {
		t.Fatal("WhatsApp admission enabled Slack bot routes")
	}
	route.WorkflowID = "other-flow"
	if ok, _ := f.api.checkWhatsAppWorkflowAccess(t.Context(), "owner", route); ok {
		t.Fatal("a different Flow ID reached the route")
	}
	route.WorkflowID = "review-flow"
	f.write(t, root+"/workflow.json", `{"id":"review-flow","kind":"relay","created_by":"owner","access":{"owners":["owner"]}}`)
	if ok, _ := f.api.checkWhatsAppWorkflowAccess(t.Context(), "reader", route); ok {
		t.Fatal("removed sharing still reached the Flow")
	}
	if len(skills.BuiltinDefinition("panel-channels").SupportingFiles) == 0 {
		t.Fatal("the shared phone controls have no procedure")
	}
}

// The transport must refuse before touching the personal phone. A nil manager
// gives an authorized Builder a distinct unavailable response without pairing
// or disconnecting a real device in a test.
func TestSharedWhatsAppControlsRequireCurrentBuilder(t *testing.T) {
	for _, product := range []string{"agentworks", "work", "relays"} {
		t.Run(product, func(t *testing.T) {
			var f *externalToolsFixture
			root := "Workflow/invoices"
			if product == "work" {
				f, _ = newExternalCrewCommandFixture(t)
				root = externalCommandCrewRoot
			} else {
				f = newExternalToolsFixture(t)
			}
			withMemoryUserDirectory(t, `{"users":[{"id":"owner","role":"editor","products":["agentworks","work","relays"]},{"id":"reader","role":"viewer","products":["agentworks","work","relays"]},{"id":"foreign","role":"editor","products":["agentworks","work","relays"]}]}`)
			if product == "relays" {
				f.write(t, root+"/workflow.json", `{"id":"control-flow","kind":"relay","created_by":"owner","access":{"owners":["owner"],"readers":["reader"]}}`)
			}
			for _, action := range []string{"whatsapp_status", "whatsapp_routing", "whatsapp_unpair"} {
				for _, user := range []string{"owner", "reader", "foreign"} {
					req := httptest.NewRequest(http.MethodPost, "/api/external/call", nil)
					req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: user, Username: user}))
					out := httptest.NewRecorder()
					f.api.externalMessagingCall(out, req, map[string]any{"action": action}, externalMessagingTarget{kind: product, path: root})
					want := http.StatusForbidden
					if user == "owner" {
						want = http.StatusServiceUnavailable
					}
					if out.Code != want {
						t.Fatalf("%s %s: status=%d, want %d: %s", user, action, out.Code, want, out.Body.String())
					}
				}
			}
		})
	}
}
