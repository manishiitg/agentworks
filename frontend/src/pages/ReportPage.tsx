import { BarChart3 } from "lucide-react";
import { ReportView } from "../components/workflow/ReportViewer";

interface ReportPageProps {
  encodedPath: string;
  ownerUid?: string;
  currentUserId?: string;
  onBack?: () => void;
}

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

function isSafeReportWorkspacePath(path: string): boolean {
  const normalized = path.replace(/\\/g, "/").replace(/^\/+/, "");
  if (!normalized || normalized.split("/").includes("..")) return false;
  if (normalized !== path) return false;
  if (normalized.startsWith("Workflow/")) return normalized.split("/").length === 2;
  const parts = normalized.split("/");
  if (normalized.startsWith("Chats/Work/projects/")) return parts.length >= 4;
  if (normalized.startsWith("Chats/Code/projects/")) return parts.length === 4 && parts[3] !== "";
  // A Code shared by its owner: the server admits the people it is shared with.
  return parts.length === 6 && parts[0] === "_users" && /^[a-zA-Z0-9_-]{1,128}$/.test(parts[1]) &&
    parts[2] === "Chats" && parts[3] === "Code" && parts[4] === "projects" && parts[5] !== "";
}

// A Code link names the owner's workspace by its logical path plus the
// owner's uid; anyone else opens it at the owner's absolute path.
function codeWorkspaceForViewer(path: string, ownerUid?: string, currentUserId?: string): string {
  if (path.startsWith("Chats/Code/projects/") && ownerUid && ownerUid !== currentUserId && /^[a-zA-Z0-9_-]{1,128}$/.test(ownerUid)) {
    return `_users/${ownerUid}/${path}`;
  }
  return path;
}

export function ReportPage({ encodedPath, ownerUid, currentUserId, onBack }: ReportPageProps) {
  const decodedPath = decodeBase64Utf8(encodedPath);
  const isValidPath = decodedPath !== null && isSafeReportWorkspacePath(decodedPath);
  const workspacePath = decodedPath === null ? null : codeWorkspaceForViewer(decodedPath, ownerUid, currentUserId);
  // Crew dashboards read the crew's private db/, so they stay owner-only.
  const isWrongPersonalAccount = Boolean(decodedPath?.startsWith("Chats/Work/") && ownerUid && ownerUid !== currentUserId);
  const requestedDocument = new URLSearchParams(window.location.search).get("document") || "db/reports/index.html";
  const documentPath = /^db\/reports\/(?!.*(?:^|\/)\.\.(?:\/|$))[^\\]+\.html$/i.test(requestedDocument)
    ? requestedDocument
    : "db/reports/index.html";

  if (!isValidPath || isWrongPersonalAccount) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-background px-4 text-foreground">
        <div className="w-full max-w-md rounded-lg border border-border bg-card p-6 text-center shadow-sm">
          <BarChart3 className="mx-auto mb-4 h-10 w-10 text-muted-foreground" />
          <h1 className="mb-2 text-lg font-semibold">{isWrongPersonalAccount ? "Dashboard unavailable" : "Invalid dashboard URL"}</h1>
          <p className="mb-4 text-sm text-muted-foreground">{isWrongPersonalAccount ? "Only the crew owner can open this dashboard. Ask them to share the underlying files instead." : "The dashboard URL must include a valid encoded workflow, Crew or Code path."}</p>
          {onBack && (
            <button type="button" onClick={onBack} className="rounded-md border border-border bg-background px-3 py-1.5 text-sm font-medium text-foreground hover:bg-muted">
              Go back
            </button>
          )}
        </div>
      </div>
    );
  }

  return (
    <div className="h-screen min-h-screen overflow-hidden bg-background text-foreground">
      <ReportView workspacePath={workspacePath!} documentPath={documentPath} onClose={onBack} />
    </div>
  );
}
