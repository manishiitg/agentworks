import { useCallback, useEffect, useId, useState } from "react";
import { workflowManifestApi } from "../../services/api";
import type {
  KnowledgebaseSource,
  KnowledgebaseSourceStatus,
} from "../../services/api-types";
import { useCanWriteWorkflow } from "../../hooks/useCanWriteWorkflow";
import { BookOpen, LockKeyhole } from "lucide-react";
import { WORKFLOW_KNOWLEDGE_SOURCES_REFRESH_EVENT } from "./workflowEvents";

/** Grantor-side control: which other workflow IDs may write into THIS
 * workflow's knowledgebase/notes/. A consumer's "write" knowledgebase_source
 * does nothing until its workflow ID appears here. */
function KBWriteGrants({
  workspacePath,
  canWrite,
}: {
  workspacePath: string;
  canWrite: boolean;
}) {
  const [loading, setLoading] = useState(true);
  const [grants, setGrants] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const load = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const result = await workflowManifestApi.getWorkflowManifest(workspacePath);
      setGrants(result.manifest.access?.allowed_kb_writers || []);
    } catch (e) {
      setError(sourceError(e, "Unable to load KB write grants"));
    } finally {
      setLoading(false);
    }
  }, [workspacePath]);
  useEffect(() => {
    void load();
  }, [load]);
  const save = async (next: string[]) => {
    setBusy(true);
    setError("");
    try {
      await workflowManifestApi.updateWorkflowManifest({
        workspace_path: workspacePath,
        kb_write_grants: next,
      });
      setGrants(next);
    } catch (e) {
      setError(sourceError(e, "Unable to update KB write grants"));
    } finally {
      setBusy(false);
    }
  };
  if (!canWrite) return null;
  return (
    <div className="space-y-2 rounded-lg border border-dashed p-3">
      <h4 className="text-sm font-semibold">
        KB write grants (this workflow's own knowledgebase)
      </h4>
      <p className="text-muted-foreground">
        Workflow IDs allowed to write into this workflow's notes.
      </p>
      {loading && (
        <p role="status" className="text-muted-foreground">
          Loading…
        </p>
      )}
      {!loading && grants.length === 0 && (
        <p className="text-muted-foreground">No external write access granted.</p>
      )}
      {grants.length > 0 && (
        <ul className="flex flex-wrap gap-1.5">
          {grants.map((id) => (
            <li
              key={id}
              className="inline-flex items-center gap-1.5 rounded-full border px-2 py-0.5"
            >
              <span className="font-mono">{id}</span>
              <button
                type="button"
                disabled={busy}
                onClick={() => void save(grants.filter((g) => g !== id))}
                aria-label={`Revoke write access for ${id}`}
                className="text-muted-foreground hover:text-foreground"
              >
                ×
              </button>
            </li>
          ))}
        </ul>
      )}
      {error && (
        <p role="alert" className="text-destructive">
          {error}
        </p>
      )}
    </div>
  );
}

function sourceError(error: unknown, fallback: string) {
  const body = (error as { response?: { data?: unknown } })?.response?.data;
  return typeof body === "string" && body.trim()
    ? body.trim()
    : error instanceof Error
      ? error.message
      : fallback;
}

export function KnowledgebaseSources({
  workspacePath,
  selected = "",
  onSelect,
  variant = "knowledge",
}: {
  workspacePath: string;
  selected?: string;
  onSelect?: (alias: string) => void;
  variant?: "knowledge" | "folders";
}) {
  const canWrite = useCanWriteWorkflow(workspacePath);
  const instanceID = useId();
  const selectID = `${instanceID}-kb-source`;
  const [loading, setLoading] = useState(true);
  const [reloadKey, setReloadKey] = useState(0);
  const [sourcesLoaded, setSourcesLoaded] = useState(false);
  const [sources, setSources] = useState<KnowledgebaseSourceStatus[]>([]);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const refresh = useCallback(async () => {
    const result =
      await workflowManifestApi.getKnowledgebaseSources(workspacePath);
    setSources(result.sources || []);
  }, [workspacePath]);
  useEffect(() => {
    let active = true;
    let request = 0;
    const reload = async () => {
      const current = ++request;
      setLoading(true);
      setError("");
      try {
        const result =
          await workflowManifestApi.getKnowledgebaseSources(workspacePath);
        if (active && current === request) {
          setSources(result.sources || []);
          setSourcesLoaded(true);
        }
      } catch (e) {
        if (active && current === request) {
          setSourcesLoaded(false);
          setError(sourceError(e, "Knowledge sources unavailable"));
        }
      } finally {
        if (active && current === request) setLoading(false);
      }
    };
    const changed = (event: Event) => {
      const detail = (
        event as CustomEvent<{ workspacePath: string; origin: string }>
      ).detail;
      if (
        detail?.workspacePath === workspacePath &&
        detail.origin !== instanceID
      )
        void reload();
    };
    setSources([]);
    setSourcesLoaded(false);
    void reload();
    window.addEventListener(WORKFLOW_KNOWLEDGE_SOURCES_REFRESH_EVENT, changed);
    return () => {
      active = false;
      window.removeEventListener(
        WORKFLOW_KNOWLEDGE_SOURCES_REFRESH_EVENT,
        changed,
      );
    };
  }, [workspacePath, instanceID, reloadKey]);
  useEffect(() => {
    if (
      sourcesLoaded &&
      !loading &&
      selected &&
      !sources.some((s) => s.alias === selected)
    )
      onSelect?.("");
  }, [sources, sourcesLoaded, loading, selected, onSelect]);
  const save = async (next: KnowledgebaseSource[]) => {
    if (variant !== "folders" || !canWrite || !sourcesLoaded) return;
    setBusy(true);
    setError("");
    try {
      await workflowManifestApi.updateWorkflowManifest({
        workspace_path: workspacePath,
        knowledgebase_sources: next,
      });
      if (selected && !next.some((s) => s.alias === selected)) onSelect?.("");
      window.dispatchEvent(
        new CustomEvent(WORKFLOW_KNOWLEDGE_SOURCES_REFRESH_EVENT, {
          detail: { workspacePath, origin: instanceID },
        }),
      );
      await refresh();
    } catch (e) {
      setError(sourceError(e, "Unable to update knowledge sources"));
    } finally {
      setBusy(false);
    }
  };
  const refs = sources.map(({ workflow_id, alias, access }) => ({
    workflow_id,
    alias,
    access,
  }));
  return (
    <section
      aria-label="Knowledge sources"
      className={
        variant === "folders"
          ? "space-y-3 rounded-lg border border-border p-4 text-xs"
          : "max-h-[50%] shrink-0 space-y-3 overflow-y-auto border-b p-3 text-xs"
      }
    >
      {variant === "folders" && (
        <>
          <div className="flex flex-wrap items-center justify-between gap-2">
            <div className="flex items-center gap-2">
              <BookOpen aria-hidden="true" className="h-4 w-4 text-primary" />
              <h3 className="text-sm font-semibold">Shared knowledge bases</h3>
              {!loading && sourcesLoaded && (
                <span className="rounded-full bg-muted px-2 py-0.5 text-muted-foreground">
                  {sources.length} attached
                </span>
              )}
            </div>
          </div>
          <p className="text-muted-foreground">
            Read context and notes from other workflows. Read-write
            attachments also need a grant below.
          </p>
          {loading && (
            <p role="status" className="text-muted-foreground">
              Loading shared knowledge…
            </p>
          )}
          {!loading && !error && sources.length === 0 && (
            <p className="rounded border border-dashed p-3 text-muted-foreground">
              No shared knowledge bases attached.
            </p>
          )}
          {sources.length > 0 && (
            <ul
              className="grid gap-2 lg:grid-cols-2"
              aria-label="Attached knowledge bases"
            >
              {sources.map((source) => (
                <li
                  key={source.alias}
                  className={`min-w-0 space-y-2 rounded-lg border p-3 ${selected === source.alias ? "border-primary/40 bg-primary/5" : "border-border bg-muted/20"}`}
                >
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <span className="font-medium text-foreground">
                      {source.label || source.workflow_id}
                    </span>
                    <span className="inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[10px] text-muted-foreground">
                      <LockKeyhole aria-hidden="true" className="h-3 w-3" />
                      {source.access === "write" ? "Read-write (notes/)" : "Read only"}
                    </span>
                  </div>
                  <div className="text-muted-foreground">
                    Source workflow · Alias:{" "}
                    <span className="font-medium text-foreground">
                      {source.alias}
                    </span>
                  </div>
                  {source.workspace_path && (
                    <div className="break-all text-muted-foreground">
                      {source.workspace_path}/knowledgebase/
                    </div>
                  )}
                  <div className="flex flex-wrap items-center gap-1 text-muted-foreground">
                    {source.available ? "Shell:" : "Shell when available:"}{" "}
                    <code className="break-all rounded bg-muted px-1.5 py-0.5">
                      $WORKFLOW_KB_{source.alias.toUpperCase()}
                    </code>
                  </div>
                  {!source.available && (
                    <p
                      role="status"
                      className="text-amber-700 dark:text-amber-300"
                    >
                      Unavailable · {source.reason || "Source unavailable"}
                    </p>
                  )}
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    {onSelect && (
                      <button
                        type="button"
                        onClick={() => onSelect(source.alias)}
                        aria-pressed={selected === source.alias}
                        className="rounded border px-2 py-1 text-foreground hover:bg-muted"
                      >
                        {selected === source.alias
                          ? "Viewing this source"
                          : "View knowledge"}
                      </button>
                    )}
                    {variant === "folders" && canWrite && (
                      <button
                        type="button"
                        disabled={busy}
                        onClick={() =>
                          void save(
                            refs.filter((s) => s.alias !== source.alias),
                          )
                        }
                        aria-label={`Detach ${source.label || source.alias} knowledge base`}
                        className="rounded border px-2 py-1 disabled:opacity-50"
                      >
                        Detach
                      </button>
                    )}
                  </div>
                </li>
              ))}
            </ul>
          )}
          {sources.length > 0 && (
            <p className="text-[11px] text-muted-foreground">
              Available sources are readable by the builder, reviewers, and
              steps with read access.
            </p>
          )}
        </>
      )}
      {variant === "knowledge" && (
        <div className="space-y-2 rounded-lg bg-muted/30 p-2.5">
          <div id={selectID} className="font-medium">
            Browsing
          </div>
          <div role="group" aria-labelledby={selectID} className="grid gap-1.5">
            {[{ alias: "", label: "Local knowledge", detail: "This workflow" }, ...sources.map((source) => ({
              alias: source.alias,
              label: source.label || source.alias,
              detail: `(${source.alias}) · ${source.available ? (source.access === "write" ? "Read-write" : "Read only") : "Unavailable"}`,
            }))].map((option) => {
              const pressed = selected === option.alias;
              return (
                <button
                  key={option.alias || "local"}
                  type="button"
                  aria-pressed={pressed}
                  onClick={() => onSelect?.(option.alias)}
                  className={`flex w-full flex-wrap items-center gap-x-2 rounded-md border px-2.5 py-1.5 text-left ${
                    pressed
                      ? "border-primary/40 bg-primary/5"
                      : "border-border bg-background hover:bg-muted"
                  }`}
                >
                  <span className="font-medium text-foreground">{option.label}</span>
                  <span className="text-muted-foreground">{option.detail}</span>
                </button>
              );
            })}
          </div>
          <p className="text-muted-foreground">
            {selected
              ? sources.find((s) => s.alias === selected)?.access === "write"
                ? `Viewing shared knowledge from ${sources.find((s) => s.alias === selected)?.label || selected}. This workflow may also contribute to its notes/.`
                : `Viewing shared knowledge from ${sources.find((s) => s.alias === selected)?.label || selected}. Read only; maintained in the source workflow.`
              : "Viewing this workflow’s local knowledge."}
          </p>
          <p className="text-[11px] text-muted-foreground">
            Manage shared knowledge bases in Setup → Connected work.
          </p>
          {sources.find((s) => s.alias === selected)?.available === false && (
            <p role="status" className="text-amber-700 dark:text-amber-300">
              Unavailable ·{" "}
              {sources.find((s) => s.alias === selected)?.reason ||
                "Source unavailable"}
            </p>
          )}
        </div>
      )}
      {variant === "folders" && (
        <KBWriteGrants workspacePath={workspacePath} canWrite={canWrite} />
      )}
      {error && (
        <div className="flex flex-wrap items-center gap-2">
          <p role="alert" className="text-destructive">
            {error}
          </p>
          {!sourcesLoaded && (
            <button
              type="button"
              disabled={loading}
              onClick={() => setReloadKey((key) => key + 1)}
              className="rounded border px-2 py-1.5"
            >
              Retry
            </button>
          )}
        </div>
      )}
    </section>
  );
}
