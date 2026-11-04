// @vitest-environment happy-dom
import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, expect, it } from "vitest";
import { SettingsCard, SettingsCount, SettingsEmpty } from "./SettingsCard";

beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
});

afterEach(() => {
  document.body.innerHTML = "";
});

async function render(node: React.ReactNode) {
  const container = document.createElement("div");
  document.body.append(container);
  const root = createRoot(container);
  await act(async () => root.render(node));
  return container;
}

it("lays out icon, title, count, actions, description, and body", async () => {
  const host = await render(
    <SettingsCard
      icon={<span data-testid="icon" />}
      title="Shared knowledge bases"
      count="0 attached"
      actions={<button type="button">Attach</button>}
      description="Read context from other workflows."
      ariaLabel="Knowledge sources"
    >
      <p>Body content</p>
    </SettingsCard>,
  );
  const section = host.querySelector("section")!;
  expect(section.getAttribute("aria-label")).toBe("Knowledge sources");
  expect(host.querySelector('[data-testid="icon"]')).not.toBeNull();
  expect(host.querySelector("h3")?.textContent).toBe("Shared knowledge bases");
  expect(host.textContent).toContain("0 attached");
  expect(host.textContent).toContain("Read context from other workflows.");
  expect(host.textContent).toContain("Body content");
});

it("omits the count pill when nothing is countable", async () => {
  const host = await render(<SettingsCard title="Name and icon" />);
  expect(host.querySelector("h3")?.textContent).toBe("Name and icon");
  expect(host.querySelector(".rounded-full")).toBeNull();
});

it("renders count and empty helpers with the standard treatment", async () => {
  const host = await render(
    <div>
      <SettingsCount>3 attached</SettingsCount>
      <SettingsEmpty>No secrets yet.</SettingsEmpty>
    </div>,
  );
  expect(host.querySelector(".rounded-full")?.textContent).toBe("3 attached");
  const empty = host.querySelector("p")!;
  expect(empty.textContent).toBe("No secrets yet.");
});
