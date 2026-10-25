// @vitest-environment happy-dom
import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, it, vi } from "vitest";

vi.mock("../components/workflow/ReportViewer", () => ({
  ReportView: ({ workspacePath, allowContentQuestions, hideToolbar, dashboardConversation, crewConversationPane }: { workspacePath: string; allowContentQuestions?: boolean; hideToolbar?: boolean; dashboardConversation?: boolean; crewConversationPane?: boolean }) => <div data-questions={String(allowContentQuestions)} data-project-conversation={String(dashboardConversation)} data-crew-conversation-pane={String(crewConversationPane)} data-toolbar-hidden={String(hideToolbar)}>Dashboard runtime {workspacePath}</div>,
}));

vi.mock("../products/work/SharedCrewWorkspace", () => ({
  SharedCrewWorkspace: ({ workspacePath, children }: { workspacePath: string; children: React.ReactNode }) => <div data-shared-agents-root={workspacePath}>{children}</div>,
}));

import { ReportPage } from "./ReportPage";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });

async function renderReport(path: string, ownerUid?: string, currentUserId?: string) {
  const bytes = new TextEncoder().encode(path);
  let binary = "";
  for (const byte of bytes) binary += String.fromCharCode(byte);
  const host = document.createElement("div");
  const root = createRoot(host);
  await act(async () => root.render(<ReportPage encodedPath={btoa(binary)} ownerUid={ownerUid} currentUserId={currentUserId} />));
  return { host, root };
}

describe("standalone report page", () => {
  it("opens a Work project in the full dashboard runtime for its owner", async () => {
    const view = await renderReport("Chats/Work/projects/demo", "work-user", "work-user");
    try {
      expect(view.host.textContent).toContain("Dashboard runtime _users/work-user/Chats/Work/projects/demo");
      expect(view.host.querySelector('[data-shared-agents-root]')?.getAttribute('data-shared-agents-root')).toBe("_users/work-user/Chats/Work/projects/demo");
      expect(view.host.querySelector('[data-questions]')?.getAttribute('data-questions')).toBe('false');
      // Managed publications resolve their own server-authorized project Ask.
      expect(view.host.querySelector('[data-project-conversation]')?.getAttribute('data-project-conversation')).toBe('true');
      expect(view.host.querySelector('[data-crew-conversation-pane]')?.getAttribute('data-crew-conversation-pane')).toBe('true');
      expect(view.host.textContent).not.toContain("Live Dashboard");
      expect(view.host.querySelector('nav')).toBeNull();
      // The return bar does not restore help, refresh or close overlays on the dashboard.
      expect(view.host.querySelector('[data-toolbar-hidden]')?.getAttribute('data-toolbar-hidden')).toBe('true');
    } finally {
      await act(async () => view.root.unmount());
    }
  });

  it.each([true, false])('shows dashboard return navigation only in Electron (desktop=%s)', async desktop => {
    const previousBridge = window.electronAPI;
    if (desktop) window.electronAPI = {};
    else delete window.electronAPI;
    try {
      for (const path of ['Workflow/example', 'invalid']) {
        const previous = window.location.href;
        window.history.replaceState({}, '', `/report?path=${btoa(path)}`);
        const popstate = vi.fn();
        window.addEventListener('popstate', popstate);
        const view = await renderReport(path);
        try {
          const link = view.host.querySelector<HTMLAnchorElement>('a[href="/dashboards"]');
          if (desktop) {
            expect(link?.textContent).toBe('Back to dashboards');
            const event = new MouseEvent('click', { button: 0, bubbles: true, cancelable: true });
            await act(async () => { link!.dispatchEvent(event) });
            expect(event.defaultPrevented).toBe(true);
            expect(window.location.pathname).toBe('/dashboards');
            expect(window.location.search).toBe('');
            expect(popstate).toHaveBeenCalledOnce();
          } else {
            expect(link).toBeNull();
            expect(view.host.querySelector('nav')).toBeNull();
            expect(popstate).not.toHaveBeenCalled();
          }
        } finally {
          await act(async () => view.root.unmount());
          window.removeEventListener('popstate', popstate);
          window.history.replaceState({}, '', previous);
        }
      }
    } finally {
      if (previousBridge) window.electronAPI = previousBridge;
      else delete window.electronAPI;
    }
  });

  it("routes a team Crew link to its owner's root for server authorization", async () => {
    const view = await renderReport("Chats/Work/projects/demo", "work-user", "other-user");
    try {
      expect(view.host.textContent).toContain("Dashboard runtime _users/work-user/Chats/Work/projects/demo");
      expect(view.host.querySelector('[data-shared-agents-root]')?.getAttribute('data-shared-agents-root')).toBe("_users/work-user/Chats/Work/projects/demo");
      expect(view.host.querySelector('[data-questions]')?.getAttribute('data-questions')).toBe('false');
    } finally {
      await act(async () => view.root.unmount());
    }
  });

  it.each([
    ["Chats/Code/projects/sample-1", "owner-1"],
    ["_users/owner-1/Chats/Code/projects/sample-1", undefined],
  ] as const)("sends shared Code link %s to its owner's root for server authorization", async (path, ownerUid) => {
    const view = await renderReport(path, ownerUid, "member-2");
    try {
      expect(view.host.textContent).toContain("Dashboard runtime _users/owner-1/Chats/Code/projects/sample-1");
      expect(view.host.querySelector('[data-project-conversation]')?.getAttribute('data-project-conversation')).toBe('true');
    } finally {
      await act(async () => view.root.unmount());
    }
  });

  it("opens a Code dashboard for its owner at the logical path", async () => {
    const view = await renderReport("Chats/Code/projects/hrms-1", "owner-1", "owner-1");
    try {
      expect(view.host.textContent).toContain("Dashboard runtime _users/owner-1/Chats/Code/projects/hrms-1");
    } finally {
      await act(async () => view.root.unmount());
    }
  });

  it("accepts a physical Code project root and rejects anything deeper", async () => {
    const ok = await renderReport("_users/owner-1/Chats/Code/projects/hrms-1", undefined, "owner-1");
    const bad = await renderReport("_users/owner-1/Chats/Code/projects/hrms-1/db", undefined, "owner-1");
    try {
      expect(ok.host.textContent).toContain("Dashboard runtime _users/owner-1/Chats/Code/projects/hrms-1");
      expect(bad.host.textContent).toContain("Invalid dashboard URL");
    } finally {
      await act(async () => { ok.root.unmount(); bad.root.unmount() });
    }
  });
});
