// @vitest-environment happy-dom
import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, it, vi } from "vitest";
vi.mock("../stores/useAuthStore", () => ({ useAuthStore: vi.fn() }));
vi.mock("../pages/Login", () => ({ Login: () => <div>App sign in</div> }));
vi.mock("../pages/AuthCallback", () => ({
  AuthCallback: () => <div>OAuth</div>,
}));
vi.mock("../pages/SharedFile", () => ({
  SharedFile: ({ encodedPath }: { encodedPath: string }) => <div>Shared asset {encodedPath}</div>,
}));
vi.mock("../pages/SharedFolder", () => ({
  SharedFolder: () => <div>Shared folder</div>,
}));
vi.mock("../pages/ReportPage", () => ({
  ReportPage: ({ encodedPath, ownerUid, currentUserId }: { encodedPath: string; ownerUid?: string; currentUserId?: string }) => (
    <div>
      Report {encodedPath} owner {ownerUid} viewer {currentUserId}
    </div>
  ),
}));
vi.mock("./WorkspaceConnectionSwitcher", () => ({
  WorkspaceConnectionSwitcher: () => null,
}));
vi.mock("./DesktopAppOnlyGate", () => ({ DesktopAppOnlyGate: () => null }));
vi.mock("../services/api", () => ({ isDesktopAppOnlyMode: () => false }));
import { useAuthStore } from "../stores/useAuthStore";
import { AuthWrapper } from "./AuthWrapper";
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
describe("Shared asset sign in", () => {
  it.each([true, false])("waits for authentication and keeps the file with multi-user mode %s", async (multi) => {
    window.history.replaceState({}, "", "/file?path=YWJj");
    const state = {
      isMultiUserMode: multi,
      isMultiUserModeChecked: true,
      isAuthenticated: false,
      isLoading: !multi,
      checkAuth: vi.fn(),
      checkAuthMode: vi.fn(),
      login: vi.fn().mockResolvedValue(undefined),
    };
    vi.mocked(useAuthStore).mockImplementation(() => state);
    const host = document.createElement("div");
    document.body.append(host);
    const root = createRoot(host);
    try {
      await act(async () => root.render(<AuthWrapper>Home</AuthWrapper>));
      expect(host.textContent).not.toContain("Shared asset");
      expect(host.textContent).toContain(multi ? "App sign in" : "Loading");
      state.isAuthenticated = true;
      state.isLoading = false;
      await act(async () => root.render(<AuthWrapper>Home</AuthWrapper>));
      expect(host.textContent).toContain("Shared asset YWJj");
      expect(window.location.pathname).toBe("/file");
      window.history.replaceState({}, "", "/file?path=ZGVm");
      await act(async () => window.dispatchEvent(new PopStateEvent("popstate")));
      expect(host.textContent).toContain("Shared asset ZGVm");
    } finally {
      await act(async () => root.unmount());
      host.remove();
      window.history.replaceState({}, "", "/");
    }
  });

  it("opens legacy path-style file links in the file-only viewer", async () => {
    window.history.replaceState({}, "", "/file/V29ya2Zsb3cvY29uZmlkYS1sb2dpbi9yZXBvcnQuaHRtbA==");
    vi.mocked(useAuthStore).mockReturnValue({
      isMultiUserMode: true,
      isMultiUserModeChecked: true,
      isAuthenticated: true,
      isLoading: false,
      checkAuth: vi.fn(),
      checkAuthMode: vi.fn(),
      login: vi.fn(),
    } as ReturnType<typeof useAuthStore>);
    const host = document.createElement("div");
    document.body.append(host);
    const root = createRoot(host);
    try {
      await act(async () => root.render(<AuthWrapper>Generic workflow with hints</AuthWrapper>));
      expect(host.textContent).toContain("Shared asset V29ya2Zsb3cvY29uZmlkYS1sb2dpbi9yZXBvcnQuaHRtbA==");
      expect(host.textContent).not.toContain("Generic workflow with hints");
    } finally {
      await act(async () => root.unmount());
      host.remove();
      window.history.replaceState({}, "", "/");
    }
  });

  it("upgrades an old report file URL to the dedicated report runtime", async () => {
    const encodedFile = btoa("Workflow/customer-login/db/reports/index.html");
    const encodedWorkspace = btoa("Workflow/customer-login");
    window.history.replaceState({}, "", `/file/${encodeURIComponent(encodedFile)}`);
    vi.mocked(useAuthStore).mockReturnValue({
      user: { id: "owner", username: "owner" },
      isMultiUserMode: true,
      isMultiUserModeChecked: true,
      isAuthenticated: true,
      isLoading: false,
      checkAuth: vi.fn(),
      checkAuthMode: vi.fn(),
      login: vi.fn(),
    } as ReturnType<typeof useAuthStore>);
    const host = document.createElement("div");
    document.body.append(host);
    const root = createRoot(host);
    try {
      await act(async () => root.render(<AuthWrapper>Generic workflow with hints</AuthWrapper>));
      expect(host.textContent).toContain(`Report ${encodedWorkspace}`);
      expect(host.textContent).not.toContain("Shared asset");
      expect(host.textContent).not.toContain("Generic workflow with hints");
      expect(window.location.pathname).toBe("/report");
      expect(new URLSearchParams(window.location.search).get("path")).toBe(encodedWorkspace);
    } finally {
      await act(async () => root.unmount());
      host.remove();
      window.history.replaceState({}, "", "/");
    }
  });

  it("preserves the Work project owner on an authenticated report link", async () => {
    window.history.replaceState({}, "", "/report?path=Q2hhdHMvV29yay9wcm9qZWN0cy9kZW1v&uid=work-user");
    vi.mocked(useAuthStore).mockReturnValue({
      user: { id: "work-user", username: "work-user" },
      isMultiUserMode: true,
      isMultiUserModeChecked: true,
      isAuthenticated: true,
      isLoading: false,
      checkAuth: vi.fn(),
      checkAuthMode: vi.fn(),
      login: vi.fn(),
    } as ReturnType<typeof useAuthStore>);
    const host = document.createElement("div");
    document.body.append(host);
    const root = createRoot(host);
    try {
      await act(async () => root.render(<AuthWrapper>Home</AuthWrapper>));
      expect(host.textContent).toContain("Report Q2hhdHMvV29yay9wcm9qZWN0cy9kZW1v owner work-user viewer work-user");
    } finally {
      await act(async () => root.unmount());
      host.remove();
      window.history.replaceState({}, "", "/");
    }
  });
});
