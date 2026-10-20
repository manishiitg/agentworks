# Retiring human_input steps

The five workflow step types are `agent`, `scripted`, `routing`, `branch` and
`crew`. The dedicated `human_input` type, its authoring tools, executor and canvas
node are removed. A remaining saved record stops with an instruction to ask Builder to replace it.

Human interaction remains available:

- Supply known values through declared run variables or typed function arguments.
  Agent descriptions render `{{VARIABLE}}` from those values. Declare
  `required_variables: ["MONTH"]` on the first consumer; blank or missing values
  stop a known selected path before work starts. Direct and dynamically reached
  consumers enforce the same requirement at their execution boundary.
- Supply operator instructions with `execute_step(human_input=...)` or
  `run_full_workflow(human_inputs={"step-id": "instructions"})`. They affect only
  that executable step, including delegated route agents; they do not rewrite
  its saved description or create an input step.
- Agents call `human_feedback` for urgent input such as CAPTCHA or OTP. Its
  bounded wait resumes the calling agent with the answer.
- Use `create_human_input_request` for a durable decision that can wait, and
  `branch` with `route_source: "human"` for a planned fixed choice.

## Updating a saved plan

Ask Builder to replace the retired question step with one of the supported
interactions above before running again. Review its routes, output dependencies
and variable references together; removing a question must not guess an answer
or approve work. Keep existing variable values and use Vault for credentials.

Check the affected routes, declared inputs, artifact consumers, script arguments
and schedule/function entry points after editing. Historical runs and saved
versions retain their original records.

See [human feedback](human_feedback_system.md) and
[communication protocols](../communication-protocols.md) for the runtime tools.
