// Package mediapresentation exposes project media and reviewable documents
// through the shared presentation store and live dashboard feed.
package mediapresentation

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/presentations"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
)

type collectionKey struct{}

func Register(registry *agentprofiles.Registry, endpoint string) error {
	for _, item := range []struct {
		id      string
		factory agentprofiles.ToolFactory
	}{
		{"media.show-video", showVideoFactory(endpoint)},
		{"media.show-character", showCharacterFactory(endpoint)},
		{"media.show-reference", showReferenceFactory(endpoint)},
		{"media.show-document", showDocumentFactory(endpoint)},
	} {
		if registry.HasToolFactory(item.id) {
			continue
		}
		factory := item.factory
		if err := registry.RegisterToolFactory(item.id, func(runtime agentprofiles.ToolRuntimeContext, raw json.RawMessage) (agentprofiles.ToolSpec, error) {
			spec, err := factory(runtime, raw)
			if err != nil {
				return spec, err
			}
			spec.Description += " Read panel-dashboard/references/media.md before use. Optional collection_id groups results in the dashboard."
			spec.Parameters["properties"].(map[string]interface{})["collection_id"] = map[string]interface{}{"type": "string", "description": "Optional stable group ID for related results."}
			execute := spec.Execute
			spec.Execute = func(ctx context.Context, args map[string]interface{}) (string, error) {
				return execute(context.WithValue(ctx, collectionKey{}, strings.TrimSpace(stringArg(args, "collection_id"))), args)
			}
			return spec, nil
		}); err != nil {
			return err
		}
	}
	return nil
}

func persistPresentation(ctx context.Context, client *workspace.Client, value presentations.Presentation) (presentations.Event, error) {
	_, err := client.InitializeWorkflowDB(ctx, workspace.InitializeWorkflowDBParams{
		DBPath: presentations.DatabasePath(value.WorkspacePath),
		Migrations: []string{
			`CREATE TABLE IF NOT EXISTS ui_presentations (id TEXT PRIMARY KEY, kind TEXT NOT NULL, schema_version INTEGER NOT NULL, session_id TEXT, title TEXT NOT NULL, payload_json TEXT NOT NULL, resources_json TEXT NOT NULL, actions_json TEXT NOT NULL, status TEXT NOT NULL, revision INTEGER NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`,
			`CREATE INDEX IF NOT EXISTS idx_ui_presentations_kind ON ui_presentations(kind)`,
		},
	})
	if err != nil {
		return presentations.Event{}, err
	}
	if collection, _ := ctx.Value(collectionKey{}).(string); collection != "" {
		value.Payload["collection_id"] = collection
		value.IdentityKey = collection + "\x1f" + value.IdentityKey
	}
	return presentations.Upsert(ctx, client, value)
}
