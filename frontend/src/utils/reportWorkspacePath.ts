// The workspace paths a dashboard (report) URL may name: a workflow, a Crew (in its owner's tree or at the shared
// root) or a Code. Shape only; the server decides who may open it.
export function isSafeReportWorkspacePath(path: string): boolean {
  const normalized = path.replace(/\\/g, "/").replace(/^\/+/, "");
  if (!normalized || normalized.split("/").includes("..")) return false;
  if (normalized !== path) return false;
  if (normalized.startsWith("Workflow/")) return normalized.split("/").length === 2;
  const parts = normalized.split("/");
  if (normalized.startsWith("Chats/Work/projects/")) return parts.length === 4 && parts[3] !== "";
  // A Crew at the shared root: the server decides who may open it (current Crew access).
  if (normalized.startsWith("Crew/")) return parts.length === 2 && parts[1] !== "" && !parts[1].startsWith(".");
  if (normalized.startsWith("Chats/Code/projects/")) return parts.length === 4 && parts[3] !== "";
  // Physical Code paths are accepted only for their owner below.
  return parts.length === 6 && parts[0] === "_users" && /^[a-zA-Z0-9_-]{1,128}$/.test(parts[1]) &&
    parts[2] === "Chats" && (parts[3] === "Code" || parts[3] === "Work") && parts[4] === "projects" && parts[5] !== "";
}
