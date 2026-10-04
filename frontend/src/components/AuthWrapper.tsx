import { useEffect, useState } from "react";
import { useAuthStore } from "../stores/useAuthStore";
import { Login } from "../pages/Login";
import { AuthCallback } from "../pages/AuthCallback";
import { MCPOAuthConsent } from "../pages/MCPOAuthConsent";
import { CLIOAuthConsent } from "../pages/CLIOAuthConsent";
import { SharedFile } from "../pages/SharedFile";
import { SharedFolder } from "../pages/SharedFolder";
import { ReportPage } from "../pages/ReportPage";
import { Loader2 } from "lucide-react";
import { WorkspaceConnectionSwitcher } from "./WorkspaceConnectionSwitcher";
import { DesktopAppOnlyGate } from "./DesktopAppOnlyGate";
import { isDesktopAppOnlyMode } from "../services/api";

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

  const [sharedFilePath, setSharedFilePath] = useState<string | null>(null);
  const [sharedFolderPath, setSharedFolderPath] = useState<string | null>(null);
  const [reportWorkspacePath, setReportWorkspacePath] = useState<string | null>(null);
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
        <WorkspaceConnectionSwitcher placement="auth" />
        <div className="text-center">
          <Loader2 className="h-8 w-8 animate-spin mx-auto text-blue-500" />
          <p className="mt-4 text-gray-600 dark:text-gray-400">Loading...</p>
        </div>
      </div>
    );
  }

  // Shared viewers use the same app authentication lifecycle. Wait for local
  // auto-login or show hosted login without losing the requested asset URL.
  if ((sharedFilePath || sharedFolderPath) && !isAuthenticated) {
    return (
      <>
        <WorkspaceConnectionSwitcher placement="auth" />
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

  // Single-user mode: no auth required, render children directly
  if (!isMultiUserMode) {
    if (isDesktopAppOnlyMode() && !window.electronAPI) {
      return <DesktopAppOnlyGate />;
    }
    if (reportWorkspacePath) {
      return (
        <ReportPage
          encodedPath={reportWorkspacePath}
          ownerUid={sharedUid || undefined}
          currentUserId={user?.id}
          onBack={() => {
            setReportWorkspacePath(null);
            window.history.pushState({}, "", "/");
          }}
        />
      );
    }
    return <>{children}</>;
  }

  // Multi-user mode: require authentication (login only, no registration)
  if (!isAuthenticated) {
    return (
      <>
        <WorkspaceConnectionSwitcher placement="auth" />
        <Login />
      </>
    );
  }


  // Authenticated: render children
  if (isDesktopAppOnlyMode() && !window.electronAPI) {
    return <DesktopAppOnlyGate />;
  }

  if (reportWorkspacePath) {
    return (
      <ReportPage
        encodedPath={reportWorkspacePath}
        ownerUid={sharedUid || undefined}
        currentUserId={user?.id}
        onBack={() => {
          setReportWorkspacePath(null);
          window.history.pushState({}, "", "/");
        }}
      />
    );
  }

  return <>{children}</>;
}
