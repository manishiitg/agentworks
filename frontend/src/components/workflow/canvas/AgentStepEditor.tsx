import { useCallback, useEffect, useRef, useState } from "react";
import { ArrowDown, ArrowUp, Plus, X } from "lucide-react";
import type {
  AgentConfigs,
  AgentItem,
  AgentPlanStep,
  PlanStep,
  PlanStepPatch,
  StepInputBinding,
} from "../../../utils/stepConfigMatching";
import { agentApi } from "../../../services/api";
import { StepEditPanel } from "../../events/orchestrator/StepEditPanel";

interface Props {
  step: AgentPlanStep;
  steps: PlanStep[];
  scripts: Array<{ id: string; title: string }>;
  agents?: Array<{ id: string; name: string }>;
  workspacePath: string;
  onSave: (patch: PlanStepPatch, config?: AgentConfigs) => Promise<void>;
  onClose: () => void;
}
const field =
  "w-full rounded-md border border-border bg-background px-3 py-2 text-sm";

// Keep complete item objects: editing a message must not drop its permissions,
// validation, repetition settings, or intentional repeated follow-ups.
export function AgentStepEditor({
  step,
  steps,
  scripts,
  agents = [],
  workspacePath,
  onSave,
  onClose,
}: Props) {
  const [tab, setTab] = useState("Instructions");
  const [task, setTask] = useState(step.description || "");
  const [items, setItems] = useState<AgentItem[]>(
    structuredClone(step.items || []),
  );
  const [agent, setAgent] = useState(step.crew_agent || "");
  const [inputs, setInputs] = useState(() =>
    Object.entries(step.inputs || {}).map(([name, binding]) => ({
      key: crypto.randomUUID(),
      name,
      binding,
    })),
  );
  const [config, setConfig] = useState(step.agent_configs);
  const [configChanged, setConfigChanged] = useState(false);
  const [inputNames, setInputNames] = useState<string[]>([]);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);
  const panel = useRef<HTMLElement>(null);
  useEffect(() => {
    let alive = true;
    agentApi
      .getVariableGroups(workspacePath)
      .then((response) => {
        if (alive)
          setInputNames(response.manifest?.variables?.map((v) => v.name) || []);
      })
      .catch(() => {
        if (alive)
          setError(
            "Could not load workflow inputs. Reopen this step to try again.",
          );
      });
    return () => {
      alive = false;
    };
  }, [workspacePath]);
  const stageConfig = useCallback((next: AgentConfigs) => {
    setConfig(next);
    setConfigChanged(true);
    setSaved(false);
  }, []);
  const updateItem = (index: number, patch: Partial<AgentItem>) =>
    setItems(
      items.map((item, i) => (i === index ? { ...item, ...patch } : item)),
    );
  const move = (index: number, delta: number) => {
    const next = [...items];
    [next[index], next[index + delta]] = [next[index + delta], next[index]];
    setItems(next);
  };
  const outputs = Array.from(
    new Set(
      steps
        .slice(0, Math.max(0, steps.findIndex((s) => s.id === step.id)))
        .flatMap((s) =>
          Array.isArray(s.context_output)
            ? s.context_output
            : s.context_output
              ? [s.context_output]
              : [],
        ),
    ),
  );
  const save = async () => {
    for (const control of panel.current?.querySelectorAll<
      HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement
    >("input,textarea,select") || []) {
      if (!control.checkValidity()) {
        setError(control.validationMessage);
        const section = control.closest<HTMLElement>("[data-step-section]")
          ?.dataset.stepSection;
        if (section) setTab(section);
        const details = control.closest("details");
        if (details) details.open = true;
        requestAnimationFrame(() => control.reportValidity());
        return;
      }
    }
    setError("");
    setSaved(false);
    setSaving(true);
    try {
      const names = inputs.map((row) => row.name.trim());
      if (names.some((name) => !name) || new Set(names).size !== names.length)
        throw new Error("Give each input a different name.");
      const dependencies = Array.from(
        new Set([
          ...(step.context_dependencies || []),
          ...inputs.flatMap((row) =>
            row.binding.output ? [row.binding.output] : [],
          ),
        ]),
      );
      await onSave(
        {
          description: task,
          items,
          crew_agent: agent,
          inputs: Object.fromEntries(
            inputs.map((row, i) => [names[i], row.binding]),
          ),
          context_dependencies: dependencies,
        },
        configChanged ? config : undefined,
      );
      setSaved(true);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not save this step.");
    } finally {
      setSaving(false);
    }
  };
  return (
    <aside
      ref={panel}
      onChangeCapture={() => setSaved(false)}
      data-ui-plan-step={step.id}
      aria-label={`Step details: ${step.title}`}
      className="flex h-full w-full flex-col border-l bg-background sm:w-[70%] sm:max-w-[70%]"
    >
      <header className="flex items-center justify-between border-b p-4">
        <h2 className="font-semibold">{step.title}</h2>
        <button aria-label="Close step details" onClick={onClose}>
          <X className="h-4 w-4" />
        </button>
      </header>
      <div
        role="tablist"
        aria-label="Step sections"
        className="flex gap-4 border-b px-4"
      >
        {["Instructions", "Inputs", "Settings"].map((name) => (
          <button
            role="tab"
            aria-selected={tab === name}
            key={name}
            onClick={() => setTab(name)}
            className={`border-b-2 py-3 text-sm ${tab === name ? "border-primary" : "border-transparent text-muted-foreground"}`}
          >
            {name}
          </button>
        ))}
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto p-4">
        <div
          data-step-section="Instructions"
          hidden={tab !== "Instructions"}
          className="space-y-5"
        >
          <label className="block space-y-2 text-sm">
            <span className="font-medium">Task</span>
            <textarea
              aria-label="Task"
              required
              className={`${field} min-h-36`}
              value={task}
              onChange={(e) => setTask(e.target.value)}
            />
          </label>
          <p className="text-xs text-muted-foreground">
            The agent starts with this task. Follow-ups continue the same
            conversation.
          </p>
          <h3 className="text-sm font-medium">Then</h3>
          {items.length === 0 && (
            <p className="text-sm text-muted-foreground">
              No follow-up actions.
            </p>
          )}
          {items.map((item, index) => (
            <div key={item.id} className="space-y-2 rounded-lg border p-3">
              <div className="flex items-center justify-between text-sm">
                <span>
                  {index + 1}.{" "}
                  {item.type === "scripted"
                    ? "Run script"
                    : item.type === "user_message" || !item.type
                      ? "Follow-up"
                      : item.type === "foreach"
                        ? "Repeat for each item"
                        : "Check results"}
                </span>
                <div className="flex gap-2">
                  <button
                    aria-label={`Move action ${index + 1} up`}
                    disabled={index === 0}
                    onClick={() => move(index, -1)}
                  >
                    <ArrowUp className="h-4 w-4" />
                  </button>
                  <button
                    aria-label={`Move action ${index + 1} down`}
                    disabled={index === items.length - 1}
                    onClick={() => move(index, 1)}
                  >
                    <ArrowDown className="h-4 w-4" />
                  </button>
                  <button
                    aria-label={`Remove action ${index + 1}`}
                    onClick={() =>
                      setItems(items.filter((_, i) => i !== index))
                    }
                  >
                    <X className="h-4 w-4" />
                  </button>
                </div>
              </div>
              {(item.type === "user_message" || !item.type) && (
                <textarea
                  aria-label={`Follow-up ${index + 1}`}
                  required
                  className={field}
                  rows={3}
                  value={item.message || ""}
                  onChange={(e) =>
                    updateItem(index, { message: e.target.value })
                  }
                />
              )}
              {item.type === "scripted" && (
                <>
                  <p className="text-xs text-muted-foreground">
                    Runs here and waits for the result. Optional script tools
                    stay under Settings.
                  </p>
                  {item.scripted_steps?.map((call, ci) => (
                    <div key={call.id} className="space-y-2">
                      <select
                        aria-label={`Script for action ${index + 1}`}
                        className={field}
                        value={call.step_id}
                        onChange={(e) =>
                          updateItem(index, {
                            scripted_steps: item.scripted_steps?.map((c, i) =>
                              i === ci ? { ...c, step_id: e.target.value } : c,
                            ),
                          })
                        }
                      >
                        {!scripts.some((s) => s.id === call.step_id) && (
                          <option value={call.step_id}>{call.step_id}</option>
                        )}
                        {scripts.map((s) => (
                          <option key={s.id} value={s.id}>
                            {s.title}
                          </option>
                        ))}
                      </select>
                      <JSONField
                        label="Script input values"
                        value={call.parameters || {}}
                        onChange={(value) =>
                          updateItem(index, {
                            scripted_steps: item.scripted_steps?.map((c, i) =>
                              ci === i ? { ...c, parameters: value } : c,
                            ),
                          })
                        }
                      />
                    </div>
                  ))}
                </>
              )}
              <details>
                <summary className="cursor-pointer text-xs text-muted-foreground">
                  Action settings
                </summary>
                <JSONField
                  label={`Action ${index + 1} settings`}
                  value={item}
                  onChange={(value) =>
                    updateItem(index, { ...value, id: item.id } as AgentItem)
                  }
                />
              </details>
            </div>
          ))}
          <div className="flex flex-wrap gap-3 text-sm">
            <button
              onClick={() =>
                setItems([
                  ...items,
                  {
                    id: crypto.randomUUID(),
                    type: "user_message",
                    message: "",
                  },
                ])
              }
              className="inline-flex items-center gap-1"
            >
              <Plus className="h-4 w-4" />
              Follow-up
            </button>
            <button
              disabled={!scripts.length}
              title={
                scripts.length
                  ? undefined
                  : "Add a saved script step through Crew chat first"
              }
              onClick={() =>
                setItems([
                  ...items,
                  {
                    id: crypto.randomUUID(),
                    type: "scripted",
                    scripted_steps: [
                      { id: crypto.randomUUID(), step_id: scripts[0].id },
                    ],
                  },
                ])
              }
              className="inline-flex items-center gap-1 disabled:opacity-50"
            >
              <Plus className="h-4 w-4" />
              Run script
            </button>
          </div>
        </div>
        <div
          data-step-section="Inputs"
          hidden={tab !== "Inputs"}
          className="space-y-4"
        >
          <p className="text-sm text-muted-foreground">
            Name the information this task receives. Earlier outputs are file
            references unless you select a JSON field.
          </p>
          {inputs.map((row, index) => {
            const kind =
              row.binding.input !== undefined
                ? "input"
                : row.binding.output !== undefined
                  ? "output"
                  : "value";
            const update = (binding: StepInputBinding) =>
              setInputs(
                inputs.map((r, i) => (i === index ? { ...r, binding } : r)),
              );
            return (
              <div key={row.key} className="space-y-2 rounded-lg border p-3">
                <div className="flex gap-2">
                  <input
                    aria-label={`Input ${index + 1} name`}
                    className={field}
                    required
                    pattern="[A-Za-z_][A-Za-z0-9_]*"
                    placeholder="Name"
                    value={row.name}
                    onChange={(e) =>
                      setInputs(
                        inputs.map((r, i) =>
                          i === index ? { ...r, name: e.target.value } : r,
                        ),
                      )
                    }
                  />
                  <button
                    aria-label={`Remove input ${index + 1}`}
                    onClick={() =>
                      setInputs(inputs.filter((_, i) => i !== index))
                    }
                  >
                    <X className="h-4 w-4" />
                  </button>
                </div>
                <select
                  aria-label={`Input ${index + 1} source`}
                  className={field}
                  value={kind}
                  onChange={(e) =>
                    update(
                      e.target.value === "input"
                        ? { input: inputNames[0] || "" }
                        : e.target.value === "output"
                          ? { output: outputs[0] || "" }
                          : { value: "" },
                    )
                  }
                >
                  <option value="value">Fixed value</option>
                  <option value="input">Workflow input</option>
                  <option value="output">Earlier step output</option>
                </select>
                {kind === "value" ? (
                  <FixedInputValue
                    label={`Input ${index + 1} value`}
                    value={row.binding.value}
                    onChange={(value) => update({ value })}
                  />
                ) : (
                  <select
                    required
                    aria-label={`Input ${index + 1} reference`}
                    className={field}
                    value={row.binding.input || row.binding.output || ""}
                    onChange={(e) =>
                      update(
                        kind === "input"
                          ? { input: e.target.value }
                          : { output: e.target.value },
                      )
                    }
                  >
                    <option value="">Choose a value</option>
                    {Array.from(
                      new Set([
                        ...(kind === "input" ? inputNames : outputs),
                        row.binding.input || row.binding.output || "",
                      ]),
                    )
                      .filter(Boolean)
                      .map((name) => (
                        <option key={name}>{name}</option>
                      ))}
                  </select>
                )}
                {kind === "output" && (
                  <input
                    aria-label={`Input ${index + 1} JSON field`}
                    className={field}
                    placeholder="JSON field (optional), e.g. totals.sales"
                    value={row.binding.field || ""}
                    onChange={(e) =>
                      update({ ...row.binding, field: e.target.value })
                    }
                  />
                )}
              </div>
            );
          })}
          <button
            className="inline-flex items-center gap-1 text-sm"
            onClick={() =>
              setInputs([...inputs, { key: crypto.randomUUID(), name: "", binding: { value: "" } }])
            }
          >
            <Plus className="h-4 w-4" />
            Add input
          </button>
          {!!step.required_variables?.length && (
            <p className="text-xs text-muted-foreground">
              Required workflow inputs: {step.required_variables.join(", ")}
            </p>
          )}
          {!!step.context_dependencies?.length && (
            <p className="text-xs text-muted-foreground">
              Available files: {step.context_dependencies.join(", ")}
            </p>
          )}
        </div>
        <div
          data-step-section="Settings"
          hidden={tab !== "Settings"}
          className="space-y-5"
        >
          <label className="block space-y-2 text-sm">
            <span>Agent</span>
            <select
              aria-label="Crew agent"
              className={field}
              value={agent}
              onChange={(e) => setAgent(e.target.value)}
            >
              <option value="">Workflow agent</option>
              {agent && !agents.some((a) => a.id === agent) && (
                <option value={agent}>{agent}</option>
              )}
              {agents.map((a) => (
                <option key={a.id} value={a.id}>
                  {a.name}
                </option>
              ))}
            </select>
          </label>
          <p className="text-xs text-muted-foreground">
            {agent
              ? "Uses this Crew agent’s model, apps and skills unless the step has an explicit override. Its chat history is not imported."
              : "Uses workflow defaults and this step’s explicit settings."}
          </p>
          {!!step.predefined_routes?.length && (
            <div>
              <h3 className="text-sm font-medium">
                Available script tools and specialists
              </h3>
              <p className="text-xs text-muted-foreground">
                The agent chooses when to call these. Edit their definitions
                through Crew chat.
              </p>
              <ul className="mt-2 space-y-1 text-sm">
                {step.predefined_routes.map((route) => (
                  <li key={route.route_id}>
                    {route.route_name || route.route_id}
                  </li>
                ))}
              </ul>
            </div>
          )}
          <StepEditPanel
            step={{
              ...step,
              context_output: Array.isArray(step.context_output)
                ? step.context_output.join(", ")
                : step.context_output,
              agent_configs: step.agent_configs,
            }}
            stepIndex={0}
            onCancel={() => {}}
            onDraftChange={stageConfig}
            onSave={async () => {}}
          />

          <details>
            <summary className="cursor-pointer text-sm">
              Output checks and task rules
            </summary>
            <pre className="mt-2 whitespace-pre-wrap break-words text-xs">
              {JSON.stringify(
                {
                  outputs: step.context_output,
                  validation: step.validation_schema,
                  completion: step.success_criteria,
                  rules: step.system_rules,
                  first_message_write_limits: step.initial_write_access,
                },
                null,
                2,
              )}
            </pre>
          </details>
        </div>
      </div>
      <footer className="space-y-2 border-t p-4">
        {error && (
          <p role="alert" className="text-sm text-destructive">
            {error}
          </p>
        )}
        {saved && (
          <p role="status" className="text-sm text-muted-foreground">
            Saved. Changes apply on the next run.
          </p>
        )}
        <button
          disabled={saving}
          className="rounded-md bg-primary px-4 py-2 text-sm text-primary-foreground disabled:opacity-50"
          onClick={() => void save()}
        >
          {saving ? "Saving…" : "Save step"}
        </button>
      </footer>
    </aside>
  );
}
function JSONField({
  label,
  value,
  onChange,
}: {
  label: string;
  value: object;
  onChange: (value: Record<string, unknown>) => void;
}) {
  const [text, setText] = useState(JSON.stringify(value, null, 2));
  const [error, setError] = useState("");
  useEffect(() => {
    if (!error) setText(JSON.stringify(value, null, 2));
  }, [value, error]);
  return (
    <label className="block space-y-1 text-xs">
      <span>{label}</span>
      <textarea
        aria-label={label}
        className={`${field} font-mono text-xs`}
        rows={4}
        value={text}
        onChange={(e) => {
          setText(e.target.value);
          try {
            const parsed = JSON.parse(e.target.value);
            if (!parsed || typeof parsed !== "object" || Array.isArray(parsed))
              throw new Error();
            onChange(parsed);
            setError("");
            e.currentTarget.setCustomValidity("");
          } catch {
            const message = "Enter a valid JSON object before saving.";
            setError(message);
            e.currentTarget.setCustomValidity(message);
          }
        }}
      />
      {error && (
        <span role="alert" className="text-destructive">
          {error}
        </span>
      )}
    </label>
  );
}

function FixedInputValue({
  label,
  value,
  onChange,
}: {
  label: string;
  value: unknown;
  onChange: (value: unknown) => void;
}) {
  const [json, setJSON] = useState(typeof value !== "string");
  const [text, setText] = useState(
    typeof value === "string" ? value : JSON.stringify(value),
  );
  const control = useRef<HTMLInputElement>(null);
  return (
    <div className="space-y-2">
      <input
        ref={control}
        aria-label={label}
        className={field}
        value={text}
        onChange={(e) => {
          setText(e.target.value);
          try {
            onChange(json ? JSON.parse(e.target.value) : e.target.value);
            e.currentTarget.setCustomValidity("");
          } catch {
            e.currentTarget.setCustomValidity(
              "Enter valid JSON, or turn off structured value to use text.",
            );
          }
        }}
      />
      <label className="flex items-center gap-2 text-xs text-muted-foreground">
        <input
          type="checkbox"
          checked={json}
          onChange={(e) => {
            const enabled = e.target.checked;
            setJSON(enabled);
            const next = enabled
              ? JSON.stringify(value)
              : typeof value === "string"
                ? value
                : JSON.stringify(value);
            setText(next || "");
            control.current?.setCustomValidity("");
            if (!enabled) onChange(next || "");
          }}
        />
        Structured value (number, boolean or JSON)
      </label>
    </div>
  );
}
