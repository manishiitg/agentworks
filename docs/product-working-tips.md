# Product working tips

Each product owns its ordered `ui.tips` list in
`agent_go/internal/<product>/product.yaml`. The shared manifest loader validates
it, and `GET /api/product-tips` returns only the resolved display definitions.
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

Tips appear only after ten seconds of continuous Working. New product viewers
get a 30-minute minimum gap until two tips have been shown in that product.
The gap then becomes one hour until four tips, two hours until six, and four
hours thereafter. Continuous work may show another tip once it becomes due.
Selection takes the first eligible unseen definition; exhausting the list
stops tips until new definitions are added or context changes.

Seen ids are shared across products for each signed-in account in that browser;
cadence counters and timestamps are per account and product. This prevents
shared tips repeating as the account switches products. Anonymous/local use
keeps its prior browser history. Signed-in users start their own history
because the previous browser-wide records cannot identify their owner.
The existing used-feature suppression remains browser-local; history is not
synced across browsers or devices.
