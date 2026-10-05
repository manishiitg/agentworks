package step_based_workflow

import (
	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/mcpagent/mcpclient"
	"testing"
)

func TestWorkshopBrowserMatchesBuilderAcrossGroupsAndWorkflows(t *testing.T) {
	parent := "browser-test-parent-" + uuid.NewString()
	common.BindSessionBrowserIsolationForSession(parent)
	defer common.ClearSessionShellConfig(parent)
	namespace := common.GetSessionShellConfig(parent).BrowserSessionNamespace
	expected := common.ResolveBrowserSessionID(parent, "main")
	controller := newAgentFactoryTestOrchestrator(t)
	controller.SetHTTPSessionID(parent)
	t.Cleanup(func() { mcpclient.GetSessionRegistry().CloseHTTPSession(parent) })
	for _, workspace := range []string{"Workflow/one", "Workflow/two"} {
		for _, group := range []string{"default", "ai-news", "parallel-worker"} {
			got := common.PrefixBrowserSessionID(workshopBrowserSessionID(namespace, workspace, group))
			if got != expected {
				t.Fatalf("%s/%s got %s; builder %s", workspace, group, got, expected)
			}
			child := parent + "/" + workspace + "/" + group + "-step"
			controller.bindWorkshopBrowserSession(child, got)
			t.Cleanup(func() { common.ClearSessionShellConfig(child) })
			if common.SandboxBrowserSession(child) != expected || mcpclient.GetSessionRegistry().HTTPSessionForMCPSession(child) != parent {
				t.Fatal("step lost its builder browser or authenticated parent registration")
			}
		}
	}
	other := workshopBrowserSessionID(common.SessionBrowserSessionNamespace("other-chat"), "Workflow/one", "default")
	if common.PrefixBrowserSessionID(other) == expected {
		t.Fatal("different sessions share a browser")
	}
}
