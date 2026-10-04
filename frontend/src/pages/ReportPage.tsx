import { BarChart3 } from "lucide-react";
import { ReportView } from "../components/workflow/ReportViewer";
import { isSafeReportWorkspacePath } from "../utils/reportWorkspacePath";

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

export function ReportPage({ encodedPath, ownerUid, currentUserId, onBack }: ReportPageProps) {
  const decodedPath = decodeBase64Utf8(encodedPath);
  const isValidPath = decodedPath !== null && isSafeReportWorkspacePath(decodedPath);
  const workspacePath = decodedPath;
  // Crew dashboards read the crew's private db/, so they stay owner-only.
  const physicalCodeOwner = decodedPath?.match(/^_users\/([^/]+)\/Chats\/Code\/projects\//)?.[1];
  const isWrongPersonalAccount = Boolean(
    ((decodedPath?.startsWith("Chats/Work/") || decodedPath?.startsWith("Chats/Code/")) && ownerUid && ownerUid !== currentUserId) ||
    (physicalCodeOwner && physicalCodeOwner !== currentUserId),
  );
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
          <p className="mb-4 text-sm text-muted-foreground">{isWrongPersonalAccount ? "Only the workspace owner can open this dashboard." : "The dashboard URL must include a valid encoded workflow, Crew or Code path."}</p>
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
