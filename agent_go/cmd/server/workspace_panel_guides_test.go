package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

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

// The transport must refuse before touching the personal phone. A nil manager
// gives an authorized Builder a distinct unavailable response without pairing
// or disconnecting a real device in a test.
func TestSharedWhatsAppControlsRequireCurrentBuilder(t *testing.T) {
	for _, product := range []string{"agentworks", "work"} {
		t.Run(product, func(t *testing.T) {
			var f *externalToolsFixture
			root := "Workflow/invoices"
			if product == "work" {
				f, _ = newExternalCrewCommandFixture(t)
				root = externalCommandCrewRoot
			} else {
				f = newExternalToolsFixture(t)
			}
			withMemoryUserDirectory(t, `{"users":[{"id":"owner","role":"editor","products":["agentworks","work"]},{"id":"reader","role":"viewer","products":["agentworks","work"]},{"id":"foreign","role":"editor","products":["agentworks","work"]}]}`)
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
