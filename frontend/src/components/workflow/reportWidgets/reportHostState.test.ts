// @vitest-environment happy-dom
import { expect, it } from "vitest";
import { readReportHostState } from "./reportHostRuntime";

function frameWith(html: string) {
  const frame = document.createElement("iframe");
  document.body.replaceChildren(frame);
  frame.contentDocument!.documentElement.setAttribute(
    "data-report-state",
    "ready",
  );
  frame.contentDocument!.body.innerHTML = html;
  return frame;
}

it("flags rendered text that looks like unrendered markdown", () => {
  const frame = frameWith(
    "<p>Placed **3** bids today</p>" +
      "<p>### Route summary</p>" +
      "<p>See [the doc](https://example.com) for detail</p>" +
      "<p>Plain sentence with *one* asterisk and a - dash.</p>" +
      "<p>Loading…</p>",
  );
  const state = readReportHostState(frame);
  expect(state.markdownTexts).toHaveLength(3);
  expect(state.markdownTexts[0]).toContain("**3**");
  expect(state.markdownTexts[1]).toContain("### Route summary");
  expect(state.markdownTexts[2]).toContain("[the doc]");
  expect(state.loadingTexts).toEqual(["Loading…"]);
});

it("flags double-underscore bold, images, and fenced blocks", () => {
  const frame = frameWith(
    "<p>Very __important__ note</p>" +
      "<p>Logo ![alt](https://example.com/logo.png) here</p>" +
      "<pre>```js\nconst x = 1\n```</pre>",
  );
  const state = readReportHostState(frame);
  expect(state.markdownTexts).toHaveLength(3);
  expect(state.markdownTexts[0]).toContain("__important__");
  expect(state.markdownTexts[1]).toContain("![alt]");
  expect(state.markdownTexts[2]).toContain("```js");
});

it("returns no markdown flags for clean rendered text", () => {
  const frame = frameWith(
    "<h1>Composed dashboard</h1><p>All routes green.</p><p>100% done (a+b).</p>",
  );
  const state = readReportHostState(frame);
  expect(state.markdownTexts).toEqual([]);
});

it("ignores source bundles and hidden text while checking rendered content", () => {
  const frame = frameWith(
    '<script>const __generated_bundle__ = "**source**";</script>' +
      '<style>:root { --label: "__source__"; }</style>' +
      '<template><p>### Template source</p></template>' +
      '<noscript><p>**Fallback source**</p></noscript>' +
      '<section hidden><p>### Hidden tab</p><p>Loading hidden data</p></section>' +
      '<p>Visible **markdown**</p><p>Loading…</p>',
  );
  const state = readReportHostState(frame);
  expect(state.markdownTexts).toEqual(["Visible **markdown**"]);
  expect(state.loadingTexts).toEqual(["Loading…"]);
});

it("truncates long flagged excerpts", () => {
  const frame = frameWith(`<p>${"word ".repeat(40)}**bold** tail</p>`);
  const state = readReportHostState(frame);
  expect(state.markdownTexts).toHaveLength(1);
  expect(state.markdownTexts[0].length).toBeLessThanOrEqual(120);
  expect(state.markdownTexts[0]).toContain("**bold**");
});
