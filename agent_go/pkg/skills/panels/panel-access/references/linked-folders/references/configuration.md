## Attached folders

- Use `files action=folders` first. Attach only an existing path permitted by the
  administrator, with a clear alias and the least access needed (`read_only`
  unless writes are required).
- Use the exact stored ID when detaching. A newly attached folder becomes part
  of the native CLI guard on the next turn; do not pretend the current turn's
  process gained access retroactively.
