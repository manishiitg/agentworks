import { ArrowLeft, BarChart3 } from "lucide-react";
import { ReportView } from "../components/workflow/ReportViewer";
import { SharedCrewWorkspace } from "../products/work/SharedCrewWorkspace";
import { isSafeReportWorkspacePath } from "../utils/reportWorkspacePath";
import { followAppLink } from "../utils/appNavigation";

interface ReportPageProps {
  encodedPath: string;
  ownerUid?: string;
  currentUserId?: string;
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

export function ReportPage({ encodedPath, ownerUid }: ReportPageProps) {
  const decodedPath = decodeBase64Utf8(encodedPath);
  const isValidPath = decodedPath !== null && isSafeReportWorkspacePath(decodedPath);
  const workspacePath = /^Chats\/(?:Work|Code)\/projects\//.test(decodedPath ?? "") && ownerUid && /^[a-zA-Z0-9_-]{1,128}$/.test(ownerUid)
    ? `_users/${ownerUid}/${decodedPath}` : decodedPath;
  // The server checks current Share access for both project origins.
  const requestedDocument = new URLSearchParams(window.location.search).get("document") || "db/reports/index.html";
  const documentPath = /^db\/reports\/(?!.*(?:^|\/)\.\.(?:\/|$))[^\\]+\.html$/i.test(requestedDocument)
    ? requestedDocument
    : "db/reports/index.html";
  const backLink = window.electronAPI ? <a href="/dashboards" onClick={followAppLink} className="inline-flex min-h-8 items-center gap-2 rounded-md px-2 text-sm text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"><ArrowLeft className="h-4 w-4" aria-hidden="true" />Back to dashboards</a> : null;

  if (!isValidPath) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-background px-4 text-foreground">
        <div className="w-full max-w-md rounded-lg border border-border bg-card p-6 text-center shadow-sm">
          <BarChart3 className="mx-auto mb-4 h-10 w-10 text-muted-foreground" />
          <h1 className="mb-2 text-lg font-semibold">Invalid dashboard URL</h1>
          <p className="mb-4 text-sm text-muted-foreground">The dashboard URL must include a valid encoded workflow, Crew or Code path.</p>
          {backLink}
        </div>
      </div>
    );
  }

  return (
    <div className="flex h-screen min-h-0 flex-col overflow-hidden bg-background text-foreground">
      {backLink && <nav aria-label="Dashboard navigation" className="flex h-10 shrink-0 items-center border-b border-border px-3">{backLink}</nav>}
      <div className="min-h-0 flex-1">
        <SharedCrewWorkspace key={workspacePath} workspacePath={workspacePath!} enabled>
          <ReportView workspacePath={workspacePath!} documentPath={documentPath} allowContentQuestions={false} dashboardConversation crewConversationPane hideToolbar />
        </SharedCrewWorkspace>
      </div>
    </div>
  );
}
