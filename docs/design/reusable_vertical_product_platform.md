# Reusable platform for dedicated agent products

Products reuse the platform's runtime and access checks while owning their
business concepts, presentation and content. Declare profiles, capabilities,
tools, commands and skills through the shared product manifest loader. See the
[manifest guide](../core/product_yaml_design_guide.md) for the current contract.

## Shared infrastructure

- The agent profile registry resolves runtime policy and tool admission.
- Shared session transcripts and chat components render the server's events.
- Workspace references, file grants and protected-path checks scope file access.
- Provider accounts, Vault grants and selected secrets use the existing account
  and project rules. Secret values do not belong in messages or source files.
- Browser sessions, voice transcription, channel transports and schedules remain
  platform services, with product-specific behavior supplied by their consumers.

## Product-owned behavior

Keep domain records, custom interfaces and profile-specific instructions in the
product module. A new product should not copy the workflow server or shared chat
renderer. Extract shared behavior when more than one real consumer needs it;
otherwise keep the implementation with the product that owns it.

## Access and execution

The backend decides product availability, project access and tool permissions.
A frontend selection cannot grant capability. Native CLI permissions and managed
workspace tools have their own enforced scopes; describe actual denials rather
than inferring one runtime's access from another runtime's label.

External channels adapt the same product entry points and permission checks.
They do not create separate authorization or execution models. A product stored
on a dedicated branch carries its own implementation and release configuration;
platform main keeps only the shared contracts it actively uses.
