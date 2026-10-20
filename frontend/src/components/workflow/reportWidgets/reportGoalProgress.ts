import type {
  GoalMetric,
  GoalObservation,
} from "../../../services/api-types";
import {
  goalMetricGroups,
  supportingMetricLabel,
  metricDimensions,
} from "../goalMetricGroups";
import { goalMetricProgress } from "../goalMetricProgress";
import type { ReportDataApi } from "./reportEmbedContext";

export interface ReportGoalMetrics {
  metrics: GoalMetric[];
  observations: GoalObservation[];
  progress: Array<
    ReturnType<typeof goalMetricProgress> & { metric: GoalMetric }
  >;
}

const quote = (value: string) => `'${value.replaceAll("'", "''")}'`;

// Use the host's scoped read-only query API in both the app and preview_report.
// Separate bounded queries prevent a busy metric from hiding another's history.
export async function getReportGoalMetrics(
  query: ReportDataApi["query"],
): Promise<ReportGoalMetrics> {
  const tables = await query(
    "SELECT name FROM sqlite_master WHERE type='table' AND name IN ('workflow_goal_metrics','pulse_goal_observations')",
  );
  if (!tables.some((t) => t.name === "workflow_goal_metrics"))
    return { metrics: [], observations: [], progress: [] };
  const rows = await query(
    "SELECT metric_id, definition_json FROM workflow_goal_metrics WHERE active = 1 ORDER BY metric_id",
  );
  const metrics = rows
    .map((row) => {
      const m = JSON.parse(String(row.definition_json)) as GoalMetric;
      if (
        !m ||
        m.id !== row.metric_id ||
        !["primary", "supporting"].includes(m.role) ||
        !["increase", "decrease", "maintain"].includes(m.direction) ||
        ![
          "id",
          "criterion_id",
          "name",
          "unit",
          "definition",
          "source",
          "window",
          "collection_frequency",
        ].every((k) => typeof m[k as keyof GoalMetric] === "string") ||
        (m.goal_id != null && typeof m.goal_id !== "string") ||
        (m.goal_name != null && typeof m.goal_name !== "string") ||
        (m.supports != null &&
          (!Array.isArray(m.supports) ||
            !m.supports.every((id) => typeof id === "string"))) ||
        (m.support_kind != null &&
          !["breakdown", "diagnostic", "guardrail"].includes(m.support_kind)) ||
        (m.dimensions != null &&
          (typeof m.dimensions !== "object" ||
            Array.isArray(m.dimensions) ||
            !Object.values(m.dimensions).every(
              (value) => typeof value === "string",
            ))) ||
        (m.route != null && typeof m.route !== "string") ||
        (m.environment != null && typeof m.environment !== "string") ||
        !Number.isFinite(m.freshness_hours) ||
        m.freshness_hours <= 0 ||
        (m.target !== undefined && !Number.isFinite(m.target))
      )
        throw new Error("Invalid goal metric definition");
      return { ...m, route: m.route || "", environment: m.environment || "" };
    })
    .sort(
      (a, b) => Number(b.role === "primary") - Number(a.role === "primary"),
    );
  const observations: GoalObservation[] = tables.some(
    (t) => t.name === "pulse_goal_observations",
  )
    ? (
        await Promise.all(
          metrics.map(async (m) => {
            const history =
              await query(`SELECT observation_id, criterion_id, metric, run_id, route, environment, value, status, unit, observed_at, window_start, window_end, evidence_json, recorded_at
        FROM pulse_goal_observations WHERE metric = ${quote(m.id)} AND criterion_id = ${quote(m.criterion_id)}
        AND unit = ${quote(m.unit)} AND COALESCE(route, '') = ${quote(m.route)} AND COALESCE(environment, '') = ${quote(m.environment)}
        ORDER BY julianday(observed_at) DESC, recorded_at DESC, observation_id DESC LIMIT 120`);
            return history.map((row) => {
              const evidence: unknown = JSON.parse(
                String(row.evidence_json || "[]"),
              );
              if (
                !Array.isArray(evidence) ||
                !evidence.every((e) => typeof e === "string")
              )
                throw new Error("Invalid goal measurement evidence");
              return {
                ...row,
                value: row.value == null ? undefined : row.value,
                evidence,
              } as unknown as GoalObservation;
            });
          }),
        )
      ).flat()
    : [];
  const now = Date.now();
  return {
    metrics,
    observations,
    progress: metrics.map((metric) => ({
      metric,
      ...goalMetricProgress(metric, observations, now),
    })),
  };
}

const renders = new WeakMap<Element, number>();
const format = (n: number) =>
  n.toLocaleString(undefined, { maximumFractionDigits: 2 });
const styles = `
:host{container-type:inline-size;display:block;color:inherit;font:14px/1.5 system-ui,sans-serif;overflow-wrap:anywhere}
*{box-sizing:border-box}h2,h3,p{margin:0}h2{font-size:18px;margin-bottom:16px}h3{font-size:15px}
.grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:12px}
article{min-width:0;border:1px solid color-mix(in srgb,currentColor 18%,transparent);border-radius:14px;padding:20px}
article.primary{grid-column:1/-1;background:color-mix(in srgb,#0ea5e9 6%,transparent)}
header{display:flex;justify-content:space-between;align-items:flex-start;flex-wrap:wrap;gap:10px}
.eyebrow{font-size:11px;text-transform:uppercase;letter-spacing:.07em;opacity:.65;margin-bottom:4px}
.state{font-size:11px;border-radius:20px;padding:4px 9px;background:color-mix(in srgb,currentColor 7%,transparent)}
.value{font-size:30px;font-weight:600;font-variant-numeric:tabular-nums;margin-top:16px}.primary .value{font-size:40px}
.unit{font-size:14px;font-weight:400;opacity:.65;margin-left:8px}.meta{font-size:12px;opacity:.7;margin-top:8px}
svg{width:100%;height:90px;display:block;color:#0ea5e9;margin-top:16px}.primary svg{height:140px}
.dates{display:flex;justify-content:space-between;font-size:11px;opacity:.65}
details{margin-top:16px;font-size:12px}summary{cursor:pointer;opacity:.75;min-height:44px;padding:12px 0}dl{margin:12px 0}dt{font-weight:600;margin-top:8px}dd{margin:0;opacity:.7}
.history{max-height:260px;overflow:auto;overscroll-behavior-inline:contain}table{width:100%;border-collapse:collapse;text-align:left;font-size:12px}th,td{padding:8px 12px 8px 0;vertical-align:top;border-bottom:1px solid color-mix(in srgb,currentColor 12%,transparent)}
.message{padding:20px;border:1px dashed color-mix(in srgb,currentColor 25%,transparent);border-radius:14px}
@container (max-width:800px){article,.message{padding:16px}.value{font-size:28px}.primary .value{font-size:34px}}
@container (max-width:620px){.grid{grid-template-columns:1fr}}
`;

// Shadow DOM contains the default styling without making report authors copy CSS.
// Each refresh replaces the widget; late fetches cannot overwrite a newer render.
export async function renderReportGoalProgress(
  doc: Document,
  query: ReportDataApi["query"],
  target: string | HTMLElement,
): Promise<ReportGoalMetrics> {
  const host =
    typeof target === "string"
      ? doc.querySelector<HTMLElement>(target)
      : target;
  if (
    !host ||
    host.ownerDocument !== doc ||
    typeof host.attachShadow !== "function"
  )
    throw new Error("Goal progress needs a container in this report");
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
    root.replaceChildren(el("style", styles), el("p", message, "message"));
  host.setAttribute("aria-busy", "true");
  showMessage("Loading goal progress…");
  try {
    const data = await getReportGoalMetrics(query);
    if (renders.get(host) !== version) return data;
    const section = el("section");
    section.setAttribute("aria-label", "Goal progress");
    section.append(el("h2", "Progress toward goals"));
    const grid = el("div", "", "grid");
    const { groups, unassigned } = goalMetricGroups(data.metrics);
    const entries: Array<{ metric: GoalMetric; container: HTMLElement }> = [];
    for (const group of groups) {
      const goal = el("section");
      goal.style.gridColumn = "1 / -1";
      goal.setAttribute("aria-label", group.name);
      goal.append(el("h2", group.name));
      for (const { metric, supporting } of group.primaries) {
        const container = el("section", "", "grid");
        container.style.marginBottom = "16px";
        container.setAttribute(
          "aria-label",
          `${metric.name} and supporting measurements`,
        );
        entries.push(
          ...[metric, ...supporting].map((metric) => ({ metric, container })),
        );
        goal.append(container);
      }
      grid.append(goal);
    }
    if (unassigned.length) {
      const container = el("section", "", "grid");
      container.style.gridColumn = "1 / -1";
      container.append(
        el(
          "p",
          "Unassigned supporting measurements. Edit goals & numbers to assign them.",
        ),
      );
      entries.push(...unassigned.map((metric) => ({ metric, container })));
      grid.append(container);
    }
    for (const { metric: m, container } of entries) {
      const p = data.progress.find((p) => p.metric.id === m.id)!;
      const card = el("article", "", m.role === "primary" ? "primary" : "");
      const header = el("header"),
        title = el("div");
      title.append(
        el(
          "p",
          m.role === "primary" ? "Primary metric" : supportingMetricLabel(m),
          "eyebrow",
        ),
        el("h3", m.name),
      );
      header.append(title, el("span", p.state, "state"));
      const value = el(
        "p",
        p.current === undefined ? "—" : format(p.current),
        "value",
      );
      value.append(el("span", m.unit, "unit"));
      const targetText =
        m.target === undefined
          ? "Target not set"
          : `Target ${m.direction === "decrease" ? "≤ " : m.direction === "increase" ? "≥ " : ""}${format(m.target)} ${m.unit}${m.target_date ? ` by ${m.target_date}` : ""}`;
      card.append(
        header,
        value,
        el("p", `${m.window} · ${targetText}`, "meta"),
      );
      if (p.delta !== undefined)
        card.append(
          el(
            "p",
            `${p.delta > 0 ? "+" : ""}${format(p.delta)} ${m.unit} since previous measurement`,
            "meta",
          ),
        );
      if (p.numeric.length > 1) {
        const svg = doc.createElementNS("http://www.w3.org/2000/svg", "svg");
        svg.setAttribute("viewBox", "0 0 600 110");
        svg.setAttribute("preserveAspectRatio", "none");
        svg.setAttribute("role", "img");
        svg.setAttribute(
          "aria-label",
          `${m.name} over time. Exact values in measurement history.`,
        );
        const values = p.numeric.map((o) => o.value!),
          low = Math.min(...values),
          high = Math.max(...values);
        const start = Date.parse(p.numeric[0].observed_at),
          end = Date.parse(p.numeric.at(-1)!.observed_at);
        const line = doc.createElementNS(svg.namespaceURI, "polyline");
        line.setAttribute(
          "points",
          p.numeric
            .map(
              (o) =>
                `${8 + (end === start ? 0.5 : (Date.parse(o.observed_at) - start) / (end - start)) * 584},${high === low ? 55 : 94 - ((o.value! - low) / (high - low)) * 80}`,
            )
            .join(" "),
        );
        line.setAttribute("fill", "none");
        line.setAttribute("stroke", "currentColor");
        line.setAttribute("stroke-width", "2");
        line.setAttribute("vector-effect", "non-scaling-stroke");
        svg.append(line);
        const dates = el("div", "", "dates");
        dates.append(
          el("span", new Date(start).toLocaleDateString()),
          el("span", new Date(end).toLocaleDateString()),
        );
        card.append(svg, dates);
      }
      card.append(
        el(
          "p",
          p.latest
            ? `Last observed ${new Date(p.latest.observed_at).toLocaleString()}${p.current === undefined && p.latest.status ? ` · ${p.latest.status}` : ""}`
            : "Connect collection to start tracking.",
          "meta",
        ),
      );
      const details = el("details"),
        dl = el("dl");
      details.append(el("summary", "Measurement details and history"));
      for (const [label, text] of [
        ["Definition", m.definition],
        ["Source", m.source],
        [
          "Collection",
          `${m.collection_frequency} · stale after ${m.freshness_hours} hours`,
        ],
        [
          "Scope",
          [m.route, m.environment, metricDimensions(m)]
            .filter(Boolean)
            .join(" · ") || "Workflow",
        ],
      ]) {
        dl.append(el("dt", label), el("dd", text));
      }
      details.append(dl);
      const scroll = el("div", "", "history"),
        table = el("table"),
        head = el("thead"),
        heading = el("tr"),
        body = el("tbody");
      table.append(el("caption", "Recent comparable measurements"));
      for (const label of ["Observed", "Value", "Evidence"])
        heading.append(el("th", label));
      head.append(heading);
      for (const o of [...p.history].reverse()) {
        const row = el("tr");
        row.append(
          el("td", new Date(o.observed_at).toLocaleString()),
          el(
            "td",
            (!o.status || o.status === "ok") && typeof o.value === "number"
              ? `${format(o.value)} ${m.unit}`
              : o.status || "Unavailable",
          ),
          el("td", o.evidence?.join(", ") || "Not recorded"),
        );
        body.append(row);
      }
      table.append(head, body);
      scroll.append(table);
      details.append(scroll);
      card.append(details);
      container.append(card);
    }
    section.append(
      data.metrics.length
        ? grid
        : el(
            "p",
            "Measurement setup needed. Ask the builder to run /setup-goals.",
            "message",
          ),
    );
    root.replaceChildren(el("style", styles), section);
    return data;
  } catch (error) {
    if (renders.get(host) === version)
      showMessage(
        "Goal progress unavailable. Refresh the report to try again.",
      );
    throw error;
  } finally {
    if (renders.get(host) === version) host.setAttribute("aria-busy", "false");
  }
}
