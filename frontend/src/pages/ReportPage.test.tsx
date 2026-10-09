// @vitest-environment happy-dom
import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, it, vi } from "vitest";

vi.mock("../components/workflow/ReportViewer", () => ({
  ReportView: ({ workspacePath }: { workspacePath: string }) => <div>Dashboard runtime {workspacePath}</div>,
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
      expect(view.host.textContent).not.toContain("Live Dashboard");
    } finally {
      await act(async () => view.root.unmount());
    }
  });

  it("routes a team Crew link to its owner's root for server authorization", async () => {
    const view = await renderReport("Chats/Work/projects/demo", "work-user", "other-user");
    try {
      expect(view.host.textContent).toContain("Dashboard runtime _users/work-user/Chats/Work/projects/demo");
    } finally {
      await act(async () => view.root.unmount());
    }
  });

  it("refuses a Code dashboard for another person, including a former share recipient", async () => {
    const view = await renderReport("Chats/Code/projects/hrms-1", "owner-1", "member-2");
    try {
      expect(view.host.textContent).toContain("Only the workspace owner");
      expect(view.host.textContent).not.toContain("Dashboard runtime");
    } finally {
      await act(async () => view.root.unmount());
    }
  });

  it("opens a Code dashboard for its owner at the logical path", async () => {
    const view = await renderReport("Chats/Code/projects/hrms-1", "owner-1", "owner-1");
    try {
      expect(view.host.textContent).toContain("Dashboard runtime Chats/Code/projects/hrms-1");
    } finally {
      await act(async () => view.root.unmount());
    }
  });

  it("refuses an absolute Code path for another person", async () => {
    const view = await renderReport("_users/owner-1/Chats/Code/projects/hrms-1", undefined, "member-2");
    try {
      expect(view.host.textContent).toContain("Only the workspace owner");
      expect(view.host.textContent).not.toContain("Dashboard runtime");
    } finally { await act(async () => view.root.unmount()); }
  });

  it("accepts only the owner's absolute Code path and rejects anything deeper", async () => {
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
