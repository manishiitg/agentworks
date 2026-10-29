import { useWorkspaceConnectionStore } from '../stores/useWorkspaceConnectionStore'
import { lazy, Suspense, useEffect, useState, useCallback } from "react";
import { useAuthStore } from "../stores/useAuthStore";
import { Login } from "../pages/Login";
import { AuthCallback } from "../pages/AuthCallback";
import { MCPOAuthConsent } from "../pages/MCPOAuthConsent";
import { CLIOAuthConsent } from "../pages/CLIOAuthConsent";
import { SharedFile } from "../pages/SharedFile";
import { SharedFolder } from "../pages/SharedFolder";
import { ReportPage } from "../pages/ReportPage";
import { DashboardShortLink } from "./DashboardShortLink";
import { Loader2 } from "lucide-react";
import { DesktopConnectConsent } from "../pages/DesktopConnectConsent";
import { DesktopConnections } from "./workspace/DesktopConnections";
import { DesktopAppOnlyGate } from "./DesktopAppOnlyGate";
import { isDesktopAppOnlyMode } from "../services/api";
import { DashboardsPageContext } from '../contexts/DashboardsPageContext';

const AppShell = lazy(() => import('./workspace/AppShell').then(module => ({ default: module.AppShell })));
const ProductRouteBoundary = lazy(() => import("./ProductRouteBoundary").then(module => ({ default: module.ProductRouteBoundary })));
const AgentBridgeApp = lazy(() => import('../products/bullpen/AgentBridgeApp'));
const MCPAccountSignIn = lazy(() => import('../pages/MCPAccountSignIn'));

interface AuthWrapperProps {
  children: React.ReactNode;
}

const REPORT_ENTRY_SUFFIX = "/db/reports/index.html";

function decodeBase64Utf8(value: string): string | null {
  try {
    let normalized = value.trim().replace(/ /g, "+").replace(/-/g, "+").replace(/_/g, "/");
    while (normalized.length % 4 !== 0) normalized += "=";
    const binary = atob(normalized);
    const bytes = Uint8Array.from(binary, (char) => char.charCodeAt(0));
    return new TextDecoder().decode(bytes);
  } catch {
    return null;
  }
}

function encodeBase64Utf8(value: string): string {
  const bytes = new TextEncoder().encode(value);
  let binary = "";
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary);
}

function reportWorkspaceFromFileLink(encodedPath: string): string | null {
  const decoded = decodeBase64Utf8(encodedPath);
  if (!decoded?.endsWith(REPORT_ENTRY_SUFFIX)) return null;
  const workspace = decoded.slice(0, -REPORT_ENTRY_SUFFIX.length);
  if (/^Workflow\/[^/]+$/.test(workspace)) return workspace;
  if (/^Chats\/Work\/projects\/[^/]+(?:\/[^/]+)*$/.test(workspace)) return workspace;
  return null;
}

export function AuthWrapper({ children }: AuthWrapperProps) {
  const { user, isAuthenticated, isMultiUserMode, isMultiUserModeChecked, isLoading, checkAuthMode, checkAuth, login } = useAuthStore();
  const activeWorkspaceId = useWorkspaceConnectionStore(state => state.activeWorkspaceId);
  const messagingOnly = useAuthStore(state => state.messagingOnly);

  const [sharedFilePath, setSharedFilePath] = useState<string | null>(null);
  const [sharedFolderPath, setSharedFolderPath] = useState<string | null>(null);
  const [reportWorkspacePath, setReportWorkspacePath] = useState<string | null>(null);
  const [dashboardCode, setDashboardCode] = useState<string | null>(null);
  const [dashboardList, setDashboardList] = useState(false);
  const openDashboardLink = useCallback((root: string, documentPath: string) => {
    const params = new URLSearchParams({ document: documentPath, path: encodeBase64Utf8(root) });
    window.history.replaceState({}, "", `/report?${params.toString()}`);
    window.dispatchEvent(new PopStateEvent("popstate"));
  }, []);
  const [sharedUid, setSharedUid] = useState<string | null>(null);
  const [isAuthCallback, setIsAuthCallback] = useState(false);
  const [isMcpConsent, setIsMcpConsent] = useState(false);
  const [isCliConsent, setIsCliConsent] = useState(false);
  const [singleUserAuthAttempted, setSingleUserAuthAttempted] = useState(false);

  // Check for shared file/folder URL or OAuth callback
  useEffect(() => {
    const parseRoute = () => {
      setSharedFilePath(null);
      setSharedFolderPath(null);
      setReportWorkspacePath(null);
      setDashboardCode(null);
      setDashboardList(false);
      setIsAuthCallback(false);
      setIsMcpConsent(false);
      setIsCliConsent(false);
      setSharedUid(null);
      const path = window.location.pathname;
      const params = new URLSearchParams(window.location.search);
      const uidParam = params.get("uid");

      // Current links use /file?path=BASE64. Older agents sometimes surfaced
      // /file/BASE64 instead, so accept that shape too rather than falling
      // through to the normal workflow shell.
      const encodedPathFor = (kind: "file" | "folder" | "report") => {
        if (path === `/${kind}`) return params.get("path");
        const prefix = `/${kind}/`;
        if (!path.startsWith(prefix) || path.length === prefix.length) return null;
        try {
          return decodeURIComponent(path.slice(prefix.length));
        } catch {
          return null;
        }
      };

      if (path === "/dashboards") {
        setDashboardList(true);
        return;
      }

      // Short dashboard link: /d/<code> (resolved after sign-in).
      const shortLink = /^\/d\/([2-9a-hj-km-np-z]{10})$/.exec(path);
      if (shortLink) {
        setDashboardCode(shortLink[1]);
        return;
      }

      // Check for shared file: /file?path=BASE64 or legacy /file/BASE64
      const filePath = encodedPathFor("file");
      if (filePath) {
        const reportWorkspace = reportWorkspaceFromFileLink(filePath);
        if (reportWorkspace) {
          const encodedWorkspace = encodeBase64Utf8(reportWorkspace);
          setReportWorkspacePath(encodedWorkspace);
          if (uidParam) setSharedUid(uidParam);
          const reportParams = new URLSearchParams({ path: encodedWorkspace });
          if (uidParam) reportParams.set("uid", uidParam);
          window.history.replaceState({}, "", `/report?${reportParams.toString()}`);
          return;
        }
        setSharedFilePath(filePath);
        if (uidParam) setSharedUid(uidParam);
        return;
      }

      // Check for shared folder: /folder?path=BASE64 or legacy /folder/BASE64
      const folderPath = encodedPathFor("folder");
      if (folderPath) {
        setSharedFolderPath(folderPath);
        if (uidParam) setSharedUid(uidParam);
        return;
      }

      // Check for dedicated workflow report URL.
      const reportPath = encodedPathFor("report");
      if (reportPath) {
        setReportWorkspacePath(reportPath);
        if (uidParam) setSharedUid(uidParam);
        return;
      }

      // Check for OAuth callback
      if (path === "/auth/callback") {
        setIsAuthCallback(true);
        return;
      }
      if ((path === "/oauth/consent" || path === "/oauth/vault")) {
        setIsMcpConsent(true);
        return;
      }
      if (path === "/oauth/cli") {
        setIsCliConsent(true);
        return;
      }
    };
    parseRoute();
    window.addEventListener("popstate", parseRoute);
    return () => window.removeEventListener("popstate", parseRoute);
  }, []);

  // Initialize auth state
  useEffect(() => {
    checkAuthMode();
    checkAuth();
  }, [checkAuthMode, checkAuth]);

  useEffect(() => {
    if (!isMultiUserModeChecked || isMultiUserMode || isAuthenticated || isLoading || singleUserAuthAttempted) {
      return;
    }
    setSingleUserAuthAttempted(true);
    login("", "").catch((error) => {
      console.error("[AUTH] Failed to initialize single-user token:", error);
    });
  }, [isMultiUserModeChecked, isMultiUserMode, isAuthenticated, isLoading, singleUserAuthAttempted, login]);

  // If handling OAuth callback, render callback component
  if (isAuthCallback) {
    return <AuthCallback />;
  }

  // Still loading auth mode or local single-user token
  if (!isMultiUserModeChecked || isLoading || (!isMultiUserMode && !isAuthenticated && !singleUserAuthAttempted)) {
    return (
      <div className="min-h-screen flex items-center justify-center bg-gray-100 dark:bg-gray-900">

        <div className="text-center">
          <Loader2 className="h-8 w-8 animate-spin mx-auto text-blue-500" />
          <p className="mt-4 text-gray-600 dark:text-gray-400">Loading...</p>
        </div>
      </div>
    );
  }

  if (window.location.pathname === '/desktop/connect') {
    return isAuthenticated ? <DesktopConnectConsent /> : <Login />;
  }
  if (activeWorkspaceId !== 'local' && !isAuthenticated) {
    return <main className="flex min-h-screen bg-background text-foreground"><aside className="w-72 border-r border-border p-3"><DesktopConnections /></aside><div className="p-8"><h1 className="text-xl font-semibold">This server is unavailable</h1><p className="mt-2 text-muted-foreground">Reconnect or choose another server in the sidebar. Your work stays on its server.</p></div></main>;
  }

  if (messagingOnly) {
    if (!isAuthenticated) return <Login />;
    if (isMcpConsent) return <MCPOAuthConsent />;
    if (isCliConsent) return <CLIOAuthConsent />;
    return <Suspense fallback={<div role="status" className="grid min-h-screen place-items-center">Opening AgentBridge…</div>}><AgentBridgeApp /></Suspense>;
  }

  // Shared viewers use the same app authentication lifecycle. Wait for local
  // auto-login or show hosted login without losing the requested asset URL.
  if ((sharedFilePath || sharedFolderPath) && !isAuthenticated) {
    return (
      <>

        <Login />
      </>
    );
  }

  // If viewing a shared file, render it directly
  if (sharedFilePath) {
    return (
      <SharedFile
        encodedPath={sharedFilePath}
        uid={sharedUid || undefined}
        onBack={() => {
          setSharedFilePath(null);
          window.history.pushState({}, "", "/");
        }}
      />
    );
  }

  // If viewing a shared folder, render it directly
  if (sharedFolderPath) {
    return (
      <SharedFolder
        encodedPath={sharedFolderPath}
        uid={sharedUid || undefined}
        onBack={() => {
          setSharedFolderPath(null);
          window.history.pushState({}, "", "/");
        }}
      />
    );
  }

  if (isMcpConsent && isAuthenticated) return <MCPOAuthConsent />;
  if (isCliConsent && isAuthenticated) return <CLIOAuthConsent />;
	const mcpSignIn = window.location.pathname.match(/^\/mcp\/elicitation\/([a-f0-9-]{36})$/);
	if (mcpSignIn && isAuthenticated) return <Suspense fallback={<p>Opening sign-in…</p>}><MCPAccountSignIn requestId={mcpSignIn[1]} /></Suspense>;

  // Single-user mode: no auth required, render children directly
  if (!isMultiUserMode) {
    if (isDesktopAppOnlyMode() && !window.electronAPI) {
      return <DesktopAppOnlyGate />;
    }
    if (dashboardCode) return <DashboardShortLink code={dashboardCode} onResolved={openDashboardLink} />;
    if (reportWorkspacePath) {
      return (
        <ReportPage
          encodedPath={reportWorkspacePath}
          ownerUid={sharedUid || undefined}
          currentUserId={user?.id}
        />
      );
    }
    return <DashboardsPageContext.Provider value={dashboardList}><Suspense fallback={<div role="status" className="grid min-h-screen place-items-center">Opening workspace…</div>}><AppShell key={`${activeWorkspaceId || 'local'}:${user?.id || 'local'}`}><Suspense fallback={<div role="status" className="grid h-full place-items-center">Opening page…</div>}><ProductRouteBoundary account={user?.id} allowedProducts={user?.allowed_products}>{children}</ProductRouteBoundary></Suspense></AppShell></Suspense></DashboardsPageContext.Provider>;
  }

  // Multi-user mode: require authentication (login only, no registration)
  if (!isAuthenticated) {
    return (
      <>

        <Login />
      </>
    );
  }


  // Authenticated: render children
  if (isDesktopAppOnlyMode() && !window.electronAPI) {
    return <DesktopAppOnlyGate />;
  }

  if (dashboardCode) return <DashboardShortLink code={dashboardCode} onResolved={openDashboardLink} />;

  if (reportWorkspacePath) {
    return (
      <ReportPage
        encodedPath={reportWorkspacePath}
        ownerUid={sharedUid || undefined}
        currentUserId={user?.id}
      />
    );
  }

  return <DashboardsPageContext.Provider value={dashboardList}><Suspense fallback={<div role="status" className="grid min-h-screen place-items-center">Opening workspace…</div>}><AppShell key={`${activeWorkspaceId || 'local'}:${user?.id || 'local'}`}><Suspense fallback={<div role="status" className="grid h-full place-items-center">Opening page…</div>}><ProductRouteBoundary account={user?.id} allowedProducts={user?.allowed_products}>{children}</ProductRouteBoundary></Suspense></AppShell></Suspense></DashboardsPageContext.Provider>;
}
