# Saved Python source

Read the `python-workflows` skill and the current source-edit schema before creating or changing a workflow. A workflow is a saved Python program with a typed script.json definition, its main.py and optional helper modules. Use the managed source writer; it applies the current Crew authoring permissions and validates the bundle. One top-level requirements.txt may declare dependencies; include files and installer options are not supported.

Declare inputs once with their types and optional defaults. The optional preset and explicit values fill those declarations; they do not choose separate work or fan out. Use the SDK's current input interface rather than inventing a plan or variables file. Secrets are selected through the current project/Vault grants and read from the script environment; never paste or print values.

Each run executes a frozen source bundle. Edit saved source before starting a new run; never patch a run snapshot or claim that a running program picked up a source edit. Ordinary Python functions may use `@step` labels for progress and checks; a label is not a separate execution target. Keep outputs under the returned run/output paths and use the managed database helpers for records. SQL setup permits the current additive create-table/create-index operations; do not author a migration file or bypass database guards.

Start only the work the person requested through its saved workflow or published-function entry, with the declared inputs. Check the stored result and logs. A successful syntax/import check is not proof of a complete run; failed execution is not permission to rewrite unrelated source, change accounts or repeat effects.
