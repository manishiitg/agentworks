package dashboards

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Compile the real shared media components for the Chromium live-refresh check.
func TestTSXMediaDashboardFixture(t *testing.T) {
	docs, root := t.TempDir(), "Crew/example"
	if err := os.MkdirAll(filepath.Join(docs, root), 0755); err != nil {
		t.Fatal(err)
	}
	source := `import React,{useState} from 'react';
import {MediaGallery} from '@agentworks/dashboard';
export async function loadData(report){return report.get('db/media.json')}
export default function Dashboard({data}){const[draft,setDraft]=useState('');return <main><input aria-label="Review note" value={draft} onChange={event=>setDraft(event.target.value)}/><h2>Videos</h2><MediaGallery items={data.filter(item=>item.kind==='media.video')}/><h2>Characters</h2><MediaGallery items={data.filter(item=>item.kind==='media.character')}/></main>}`
	out, err := Execute(context.Background(), docs, Request{Root: root, Action: "create", ID: "media", Title: "Live results", Files: map[string]string{"index.tsx": source}, IncludeDraft: true})
	if err != nil {
		t.Fatal(err)
	}
	if fixture := os.Getenv("DASHBOARD_MEDIA_TEST_FIXTURE"); fixture != "" {
		page, err := os.ReadFile(filepath.Join(docs, root, Document("media", out.Dashboard.Revision)))
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(fixture, page, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
