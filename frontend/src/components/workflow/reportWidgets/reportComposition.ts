// Composition widgets for HTML reports (PLAT-335 pilot): the two most
// hand-rolled dashboard patterns — data tables and the policy-required
// activity feed — as optional window.report methods.
//
// Same contract as renderGoalProgress/renderCosts: an empty container, Shadow
// DOM styling so authors copy no CSS, contents replaced on refresh, data
// returned, rejection on load failure, identical behavior in the app and
// preview_report through the shared host runtime. Plain DOM, no React.

import type { ReportDataApi } from "./reportEmbedContext";

export interface ReportTableOptions {
  /** Read-only SQL. Columns are inferred from the returned rows. */
  query: string;
  /** Positional values for SQL placeholders. */
  params?: unknown[];
  /** Show a filter box that matches every cell. Default false. */
  searchable?: boolean;
  /** Make headers toggle ascending/descending sort. Default false. */
  sortable?: boolean;
}

export interface ReportActivityOptions {
  /** Max entries. Integer 1-100, default 30. */
  limit?: number;
}

type Query = ReportDataApi["query"];

const renders = new WeakMap<Element, number>();

function stringify(value: unknown): string {
  if (value == null) return "—";
  if (typeof value === "string") return value === "" ? "—" : value;
  if (typeof value === "number" || typeof value === "boolean")
    return String(value);
  try {
    const text = JSON.stringify(value);
    return text === undefined ? "—" : text;
  } catch {
    return String(value);
  }
}

function parseJsonArray(value: unknown): Array<Record<string, unknown>> {
  if (Array.isArray(value))
    return value.filter(
      (item): item is Record<string, unknown> =>
        !!item && typeof item === "object" && !Array.isArray(item),
    );
  if (typeof value !== "string" || value.trim() === "") return [];
  try {
    const parsed: unknown = JSON.parse(value);
    return Array.isArray(parsed)
      ? parsed.filter(
          (item): item is Record<string, unknown> =>
            !!item && typeof item === "object" && !Array.isArray(item),
        )
      : [];
  } catch {
    return [];
  }
}

const tableStyles = `
:host{container-type:inline-size;display:block;color:inherit;font:14px/1.5 system-ui,sans-serif;overflow-wrap:anywhere}
*{box-sizing:border-box}
.search{display:block;width:100%;min-height:44px;margin:0 0 12px;padding:10px 12px;font:inherit;color:inherit;background:transparent;border:1px solid color-mix(in srgb,currentColor 25%,transparent);border-radius:10px}
.scroll{overflow:auto;overscroll-behavior-inline:contain;border:1px solid color-mix(in srgb,currentColor 18%,transparent);border-radius:14px}
table{width:100%;border-collapse:collapse;text-align:left;font-size:13px}
caption{text-align:left;font-size:12px;opacity:.7;padding:12px 12px 8px}
th,td{padding:10px 12px;vertical-align:top;border-bottom:1px solid color-mix(in srgb,currentColor 12%,transparent);white-space:nowrap}
tbody tr:last-child th,tbody tr:last-child td{border-bottom:0}
thead th{position:sticky;top:0;background:color-mix(in srgb,currentColor 7%,transparent);font-weight:600}
th.num,td.num{text-align:right;font-variant-numeric:tabular-nums}
th button{all:unset;display:flex;align-items:center;gap:6px;min-height:44px;margin:-10px -12px;padding:10px 12px;cursor:pointer;font:inherit;font-weight:600}
th button:focus-visible{outline:2px solid hsl(var(--ring,222 89% 55%));outline-offset:-2px}
th .arrow{font-size:11px;opacity:.7}
.message{padding:20px;border:1px dashed color-mix(in srgb,currentColor 25%,transparent);border-radius:14px}
.count{font-size:12px;opacity:.7;margin-top:8px}
@container (max-width:620px){th,td{padding:8px 10px;font-size:12px}.message{padding:16px}}
`;

// Themed, responsive table with an empty state. Kills the most repeated code
// in the fleet: query loops, row-html builders, hand-wired search and sort.
export async function renderReportTable(
  doc: Document,
  query: Query,
  target: string | HTMLElement,
  options: ReportTableOptions,
): Promise<Record<string, unknown>[]> {
  const host =
    typeof target === "string"
      ? doc.querySelector<HTMLElement>(target)
      : target;
  if (
    !host ||
    host.ownerDocument !== doc ||
    typeof host.attachShadow !== "function"
  )
    throw new Error("Table needs a container in this report");
  const sql = options?.query;
  if (typeof sql !== "string" || sql.trim() === "")
    throw new Error("renderTable needs options.query with read-only SQL");
  const searchable = options.searchable === true;
  const sortable = options.sortable === true;
  const version = (renders.get(host) || 0) + 1;
  renders.set(host, version);
  const root = host.shadowRoot || host.attachShadow({ mode: "open" });
  const el = <K extends keyof HTMLElementTagNameMap>(
    tag: K,
    text = "",
    cls = "",
  ) => {
    const node = doc.createElement(tag);
    node.textContent = text;
    if (cls) node.className = cls;
    return node;
  };
  const showMessage = (message: string) =>
    root.replaceChildren(el("style", tableStyles), el("p", message, "message"));
  host.setAttribute("aria-busy", "true");
  showMessage("Loading table…");
  try {
    const rows = options.params === undefined ? await query(sql) : await query(sql, options.params);
    if (renders.get(host) !== version) return rows;
    if (!Array.isArray(rows) || rows.length === 0) {
      showMessage("No rows returned for this table.");
      return [];
    }
    const columns: string[] = [];
    const seen = new Set<string>();
    for (const row of rows) {
      if (!row || typeof row !== "object") continue;
      for (const key of Object.keys(row)) {
        if (!seen.has(key)) {
          seen.add(key);
          columns.push(key);
        }
      }
    }
    if (columns.length === 0) {
      showMessage("No columns found in this table's rows.");
      return [];
    }
    const numeric = new Set<string>();
    for (const col of columns) {
      let sawNumeric = false;
      let sawOther = false;
      for (const row of rows) {
        const v = (row as Record<string, unknown>)[col];
        if (v == null || v === "") continue;
        if (typeof v === "number" && Number.isFinite(v)) sawNumeric = true;
        else if (
          typeof v === "string" &&
          v.trim() !== "" &&
          Number.isFinite(Number(v))
        )
          sawNumeric = true;
        else {
          sawOther = true;
          break;
        }
      }
      if (sawNumeric && !sawOther) numeric.add(col);
    }
    const wrap = el("section");
    wrap.setAttribute("aria-label", "Report table");
    let visible = [...rows];
    let sortCol = "";
    let sortDir: "asc" | "desc" = "asc";
    const scroll = el("div", "", "scroll");
    const table = el("table");
    const head = el("thead");
    const headRow = el("tr");
    const body = el("tbody");
    const count = el("p", "", "count");
    const paintRows = () => {
      body.replaceChildren();
      for (const row of visible) {
        const tr = el("tr");
        for (const col of columns) {
          const td = el(
            "td",
            stringify((row as Record<string, unknown>)[col]),
            numeric.has(col) ? "num" : "",
          );
          tr.append(td);
        }
        body.append(tr);
      }
      count.textContent =
        visible.length === rows.length
          ? `${rows.length} row${rows.length === 1 ? "" : "s"}`
          : `${visible.length} of ${rows.length} rows`;
    };
    const applySort = () => {
      if (!sortCol) {
        paintRows();
        return;
      }
      const dir = sortDir === "asc" ? 1 : -1;
      const isNum = numeric.has(sortCol);
      visible = [...visible].sort((a, b) => {
        const av = (a as Record<string, unknown>)[sortCol];
        const bv = (b as Record<string, unknown>)[sortCol];
        if (av == null && bv == null) return 0;
        if (av == null) return 1;
        if (bv == null) return -1;
        if (isNum) return (Number(av) - Number(bv)) * dir;
        return String(av).localeCompare(String(bv)) * dir;
      });
      paintRows();
    };
    for (const col of columns) {
      const th = el("th");
      th.setAttribute("scope", "col");
      if (numeric.has(col)) th.className = "num";
      if (!sortable) {
        th.textContent = col;
      } else {
        const button = el("button");
        button.type = "button";
        button.setAttribute("aria-label", `Sort by ${col}`);
        const label = el("span", col);
        const arrow = el("span", "", "arrow");
        arrow.setAttribute("aria-hidden", "true");
        button.append(label, arrow);
        button.addEventListener("click", () => {
          if (sortCol === col) sortDir = sortDir === "asc" ? "desc" : "asc";
          else {
            sortCol = col;
            sortDir = "asc";
          }
          for (const other of headRow.querySelectorAll("th")) {
            const otherBtn = other.querySelector("button");
            if (!otherBtn) continue;
            const active = otherBtn === button;
            other.setAttribute(
              "aria-sort",
              active ? (sortDir === "asc" ? "ascending" : "descending") : "none",
            );
            const otherArrow = otherBtn.querySelector(".arrow");
            if (otherArrow)
              otherArrow.textContent = active
                ? sortDir === "asc"
                  ? "▲"
                  : "▼"
                : "";
          }
          applySort();
        });
        th.setAttribute("aria-sort", "none");
        th.append(button);
      }
      headRow.append(th);
    }
    head.append(headRow);
    table.append(head, body);
    scroll.append(table);
    if (searchable) {
      const box = el("input", "", "search");
      box.type = "search";
      box.setAttribute("aria-label", "Filter table rows");
      box.setAttribute("placeholder", "Filter rows…");
      box.addEventListener("input", () => {
        const needle = box.value.trim().toLowerCase();
        visible =
          needle === ""
            ? [...rows]
            : rows.filter((row) =>
                columns.some((col) =>
                  stringify((row as Record<string, unknown>)[col])
                    .toLowerCase()
                    .includes(needle),
                ),
              );
        applySort();
      });
      wrap.append(box);
    }
    paintRows();
    wrap.append(scroll, count);
    root.replaceChildren(el("style", tableStyles), wrap);
    return rows;
  } catch (error) {
    if (renders.get(host) === version)
      showMessage("Table unavailable. Refresh the report to try again.");
    throw error;
  } finally {
    if (renders.get(host) === version) host.setAttribute("aria-busy", "false");
  }
}

const activityStyles = `
:host{container-type:inline-size;display:block;color:inherit;font:14px/1.5 system-ui,sans-serif;overflow-wrap:anywhere}
*{box-sizing:border-box}h2,h3,p{margin:0}
.list{display:flex;flex-direction:column;gap:12px}
article{min-width:0;border:1px solid color-mix(in srgb,currentColor 18%,transparent);border-radius:14px;padding:20px}
header{display:flex;justify-content:space-between;align-items:flex-start;flex-wrap:wrap;gap:10px}
h3{font-size:15px}
.meta{font-size:12px;opacity:.7;margin-top:4px}
.badge{font-size:11px;border-radius:20px;padding:4px 9px;background:color-mix(in srgb,currentColor 7%,transparent);white-space:nowrap}
.badge.failed{background:color-mix(in srgb,hsl(var(--destructive,0 84% 60%)) 12%,transparent);color:hsl(var(--destructive,0 84% 60%))}
.body{margin-top:12px;display:flex;flex-direction:column;gap:12px}
.route{border-top:1px solid color-mix(in srgb,currentColor 12%,transparent);padding-top:12px}
.routename{font-size:12px;opacity:.75}
.fields{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:8px}
.field{min-width:0;font-size:12px}
.field b{display:block;font-size:11px;text-transform:uppercase;letter-spacing:.06em;opacity:.65;margin-bottom:2px}
.section{font-size:13px}
.section b{display:block;margin-bottom:2px}
details{font-size:13px}
summary{cursor:pointer;opacity:.8;min-height:44px;padding:12px 0}
.scope{font-size:12px;opacity:.75;border:1px dashed color-mix(in srgb,currentColor 25%,transparent);border-radius:10px;padding:10px 12px}
.md{font-size:13px;line-height:1.6}
.md h1,.md h2,.md h3{line-height:1.3;margin:.8em 0 .3em}
.md h1{font-size:1.2em}.md h2{font-size:1.1em}.md h3{font-size:1em}
.md p,.md ul,.md ol{margin:.4em 0}
.md ul,.md ol{padding-left:1.3em}
.md code{font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:.88em}
.md pre{overflow:auto;padding:.7em 1em;border-radius:8px;background:color-mix(in srgb,currentColor 7%,transparent)}
.md table{border-collapse:collapse;width:100%;font-size:.9em}
.md th,.md td{border:1px solid color-mix(in srgb,currentColor 18%,transparent);padding:.3em .5em;text-align:left}
.message{padding:20px;border:1px dashed color-mix(in srgb,currentColor 25%,transparent);border-radius:14px}
@container (max-width:620px){article{padding:16px}.fields{grid-template-columns:1fr}}
`;

// The policy-required activity tab, zero config: run and Pulse summaries from
// org_dashboard_notifications, route-grouped via route_summaries_json and
// markdown-rendered. Falls back to background_agent_log, then to a setup
// message when neither history table exists (same precedent as the goal
// widget: missing tables are "not set up yet", not a failure).
export async function renderReportActivity(
  doc: Document,
  dataApi: ReportDataApi,
  target: string | HTMLElement,
  options?: ReportActivityOptions,
): Promise<Record<string, unknown>[]> {
  const host =
    typeof target === "string"
      ? doc.querySelector<HTMLElement>(target)
      : target;
  if (
    !host ||
    host.ownerDocument !== doc ||
    typeof host.attachShadow !== "function"
  )
    throw new Error("Activity needs a container in this report");
  const limit = options?.limit ?? 30;
  if (!Number.isInteger(limit) || limit < 1 || limit > 100)
    throw new Error("renderActivity needs options.limit as an integer 1-100");
  const version = (renders.get(host) || 0) + 1;
  renders.set(host, version);
  const root = host.shadowRoot || host.attachShadow({ mode: "open" });
  const el = <K extends keyof HTMLElementTagNameMap>(
    tag: K,
    text = "",
    cls = "",
  ) => {
    const node = doc.createElement(tag);
    node.textContent = text;
    if (cls) node.className = cls;
    return node;
  };
  const showMessage = (message: string) =>
    root.replaceChildren(
      el("style", activityStyles),
      el("p", message, "message"),
    );
  host.setAttribute("aria-busy", "true");
  showMessage("Loading recent activity…");
  const words = (value: unknown) =>
    String(value ?? "neutral").replace(/[_-]+/g, " ").trim() || "neutral";
  const failed = (value: unknown) =>
    /fail|error/.test(String(value ?? "").toLowerCase());
  const when = (value: unknown) => {
    const date = new Date(String(value ?? ""));
    return Number.isNaN(date.getTime())
      ? stringify(value === "" ? "Time not recorded" : value)
      : date.toLocaleString(undefined, {
          dateStyle: "medium",
          timeStyle: "short",
        });
  };
  // Markdown comes from the host renderer (trusted, same as getHtml), but
  // links cannot work inside widget shadow DOM: the host's frame-level click
  // interceptor cannot see retargeted shadow targets, and the sandbox blocks
  // popups. Keep the link text with its destination as a tooltip instead of
  // a dead control.
  const markdown = (value: unknown) => {
    const text = String(value ?? "");
    if (text.trim() === "") return null;
    const wrap = el("div", "", "md");
    wrap.innerHTML = dataApi.renderMarkdown(text);
    for (const link of wrap.querySelectorAll("a")) {
      const span = el("span", link.textContent ?? "");
      const href = link.getAttribute("href");
      if (href) span.title = href;
      link.replaceWith(span);
    }
    return wrap;
  };
  const fields = (value: unknown, legacyRoute: boolean) => {
    const items = parseJsonArray(value);
    if (items.length === 0) return null;
    const wrap = el("div", "", "fields");
    for (const item of items) {
      let label = String(item.label ?? "Detail");
      if (legacyRoute && label.toLowerCase() === "route")
        label = "Legacy Route";
      const cell = el("div", "", "field");
      cell.append(el("b", label), el("span", stringify(item.value)));
      wrap.append(cell);
    }
    return wrap;
  };
  const sections = (value: unknown) => {
    const items = parseJsonArray(value);
    if (items.length === 0) return null;
    const wrap = el("div");
    for (const item of items) {
      const heading = String(item.heading ?? "");
      const bodyText = stringify(item.body);
      if (heading === "" && (bodyText === "—" || bodyText === "")) continue;
      const section = el("div", "", "section");
      if (heading !== "") section.append(el("b", heading));
      if (bodyText !== "—" && bodyText !== "")
        section.append(el("span", bodyText));
      wrap.append(section);
    }
    return wrap.childElementCount ? wrap : null;
  };
  try {
    const tables = await dataApi.query(
      "SELECT name FROM sqlite_master WHERE type='table' AND name IN ('org_dashboard_notifications','background_agent_log')",
    );
    if (renders.get(host) !== version) return [];
    const names = new Set(tables.map((t) => String(t.name)));
    if (!names.has("org_dashboard_notifications")) {
      if (!names.has("background_agent_log")) {
        showMessage(
          "No activity yet. Run summaries appear here after the first workflow run.",
        );
        return [];
      }
      const logs = await dataApi.query(
        `SELECT name, status, result, completed_at, updated_at FROM background_agent_log WHERE kind IN ('workflow_step','message_sequence_item') ORDER BY COALESCE(completed_at, updated_at) DESC LIMIT ${limit}`,
      );
      if (renders.get(host) !== version) return logs;
      if (logs.length === 0) {
        showMessage(
          "No activity yet. Run summaries appear here after the first workflow run.",
        );
        return [];
      }
      const list = el("div", "", "list");
      list.setAttribute("aria-label", "Recent activity");
      for (const row of logs) {
        const card = el("article");
        const header = el("header");
        const title = el("div");
        title.append(
          el("h3", stringify(row.name)),
          el(
            "p",
            when(row.completed_at ?? row.updated_at),
            "meta",
          ),
        );
        const badge = el("span", words(row.status), "badge");
        if (failed(row.status)) badge.classList.add("failed");
        header.append(title, badge);
        card.append(header);
        const firstLine = String(row.result ?? "")
          .replace(/\[[^\]]+\]\([^)]*\)/g, "")
          .replace(/https?:\/\/\S+/g, "")
          .replace(/`[^`]*`/g, "")
          .replace(/\s+/g, " ")
          .trim()
          .split(/(?<=[.!?])\s/)[0];
        if (firstLine) card.append(el("p", firstLine, "body"));
        list.append(card);
      }
      root.replaceChildren(el("style", activityStyles), list);
      return logs;
    }
    let rows: Record<string, unknown>[];
    try {
      rows = await dataApi.query(
        `SELECT id, notification_kind, title, status, summary_text, message, fields_json, sections_json, route_summaries_json, created_at FROM org_dashboard_notifications WHERE notification_kind IN ('run_summary','pulse_summary') ORDER BY created_at DESC LIMIT ${limit}`,
      );
    } catch {
      // Older schema without the newer detail columns.
      rows = await dataApi.query(
        `SELECT id, notification_kind, title, status, message, fields_json, sections_json, created_at FROM org_dashboard_notifications WHERE notification_kind IN ('run_summary','pulse_summary') ORDER BY created_at DESC LIMIT ${limit}`,
      );
      for (const row of rows) {
        row.summary_text = "";
        row.route_summaries_json = "[]";
      }
    }
    if (renders.get(host) !== version) return rows;
    if (rows.length === 0) {
      showMessage(
        "No activity yet. Run summaries appear here after the first workflow run.",
      );
      return [];
    }
    const list = el("div", "", "list");
    list.setAttribute("aria-label", "Recent activity");
    for (const row of rows) {
      const kind =
        row.notification_kind === "pulse_summary"
          ? "Pulse review"
          : "Run summary";
      const card = el("article");
      const header = el("header");
      const title = el("div");
      title.append(
        el("h3", stringify(row.title) === "—" ? kind : String(row.title)),
        el("p", `${kind} · ${when(row.created_at)}`, "meta"),
      );
      const badge = el("span", words(row.status), "badge");
      if (failed(row.status)) badge.classList.add("failed");
      header.append(title, badge);
      const body = el("div", "", "body");
      const routes = parseJsonArray(row.route_summaries_json).filter(
        (route) => route.routing_step_id && route.route_id,
      );
      if (routes.length > 0) {
        const sharedWrap = el("div");
        const sharedSummary = markdown(row.summary_text);
        if (sharedSummary) sharedWrap.append(sharedSummary);
        const sharedFields = fields(row.fields_json, false);
        if (sharedFields) sharedWrap.append(sharedFields);
        const sharedSections = sections(row.sections_json);
        if (sharedSections) sharedWrap.append(sharedSections);
        if (sharedWrap.childElementCount > 0) {
          const shared = el("div");
          shared.append(el("p", "Shared workflow update", "routename"));
          shared.append(sharedWrap);
          body.append(shared);
        }
        for (const route of routes) {
          const section = el("section", "", "route");
          const routeHeader = el("header");
          const routeTitle = el("div");
          routeTitle.append(
            el("p", stringify(route.label), "routename"),
            el("h3", stringify(route.title)),
          );
          const routeBadge = el("span", words(route.status), "badge");
          if (failed(route.status)) routeBadge.classList.add("failed");
          routeHeader.append(routeTitle, routeBadge);
          section.append(routeHeader);
          const routeBody = el("div", "", "body");
          const routeMessage = markdown(route.message);
          if (routeMessage) routeBody.append(routeMessage);
          const routeFields = fields(route.fields, false);
          if (routeFields) routeBody.append(routeFields);
          const routeSections = sections(route.sections);
          if (routeSections) routeBody.append(routeSections);
          const details = el("details");
          details.append(el("summary", "Route details"));
          details.append(routeBody);
          section.append(details);
          body.append(section);
        }
      } else {
        const scope = el(
          "p",
          "Route scope not recorded. This historical summary is shown exactly as saved.",
          "scope",
        );
        body.append(scope);
        const message = markdown(row.message);
        if (message) body.append(message);
        const legacyFields = fields(row.fields_json, true);
        if (legacyFields) body.append(legacyFields);
        const legacySections = sections(row.sections_json);
        if (legacySections) body.append(legacySections);
      }
      card.append(header, body);
      list.append(card);
    }
    root.replaceChildren(el("style", activityStyles), list);
    return rows;
  } catch (error) {
    if (renders.get(host) === version)
      showMessage("Activity unavailable. Refresh the report to try again.");
    throw error;
  } finally {
    if (renders.get(host) === version) host.setAttribute("aria-busy", "false");
  }
}
