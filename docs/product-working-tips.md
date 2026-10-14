# Product working tips

Each product owns its ordered `ui.tips` list in
`agent_go/internal/<product>/product.yaml`. The shared manifest loader validates
it, and `GET /api/product-tips` returns only the resolved display definitions.
The endpoint serves only products enabled on the installation and allowed for
the caller, from a catalog assembled and conflict-checked once per process.
No frontend tip text or executable eligibility callbacks are needed.

```yaml
ui:
  tips:
    - id: quick-switcher # Shared reference: id only.
    - id: code-new-tab
      text: 'Open another chat with Alt+Shift+T.'
      skip_if_used: chat-tab-new
      when: {code_tabs: {max: 1}}
    - id: toolbar-hide
```

Shared text lives in `agent_go/pkg/agentprofiles/working_tips.yaml`; products
opt in by id. Full definitions need a stable id and nonempty text. Unknown
fields, duplicate ids, unknown shared references, conflicting definitions and
invalid tab ranges are rejected. `skip_if_used` names a tracked frontend
feature: quick-switcher, panel-switcher, toolbar-hide, chat-tab-keys or
chat-tab-new. `when.code_tabs.min` and `.max` are optional inclusive bounds.
Keep an id when editing its wording so an already-seen tip stays suppressed.

After ten seconds of continuous Working in a turn the app asks the server for
that turn's tip (`GET /api/product-tips/next?surface=...&code_tabs=...&used=...&turn=...`)
and shows it under the working line as `└ Tip: ...` for the rest of the turn. The
SERVER chooses: tips the person has never seen come first, in YAML order; a shown
tip stays off for three days; a tip is skipped when the browser reports its
`skip_if_used` feature as used or the Code tab count is out of range; when
everything is cooling down no tip is shown. The same turn asking again gets the same
tip. The choice and the shown times are stored per signed-in person on the server
(`preferences/product-tips.json` in their workspace area), so they follow the
person across browsers. A person can turn tips off in the account menu (`Tips: on/off`,
`GET/PUT /api/product-tips/preference`). The browser sends only facts only it has (open
Code tabs, features used) and holds no selection logic or tip text.
