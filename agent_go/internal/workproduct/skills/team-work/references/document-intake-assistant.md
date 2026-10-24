# Document Intake Assistant

This skill gives one Crew the Document Intake Assistant capability. It can seed a new Crew or be added to a compatible Crew. Use the customer’s systems as sources of record and reviewed action destinations.

## First useful result

1. Confirm authorized document set, type, source revision, allowed fields, retention, privacy and reviewer.
2. Extract each field with page/span evidence; distinguish printed value, inferred normalization, and missing value.
3. Validate types, totals, dates, identities and cross-field rules against the owner-approved schema.
4. Check duplicate keys and current destination records; keep low-confidence or conflicting fields in a review queue.
5. Return a reviewable structured record; create a destination record only after exact approval and provider receipt.

Deliver **A source-linked extraction with document and version ID, page or span references, field values and confidence, validation failures, duplicate state, and reviewer queue.** A field is not verified merely because OCR produced text; show source span, validation rule, uncertainty, and document version.

## Source probe and setup acceptance

Read one authorized document by hash and version with page or span evidence, its approved schema revision, and the current destination duplicate keys. Check required identity fields and arithmetic against the original file, not OCR text alone.

Return a needs-review record when a required vendor identity is absent or duplicate state cannot be resolved, even if totals add up. The setup remains pending if it fills a missing field, calls OCR verified without a source span, or writes a destination record without approval and receipt.

## Follow-through

Use source document hash and destination ID, reprocess changed versions intentionally, retain review corrections, and avoid duplicate writes.

## Fictional worked example

Fictional input: file=invoice-88.pdf hash=abc123; schema=vendor-invoice-v3 requiring vendor ID, invoice number, currency, net, tax and total; destination=accounts-payable queue.

Fictional output: invoice number INV-88 page1 line4; net=100.00 USD page1 line15; tax=8.00 USD page1 line16; total=108.00 USD page1 line17, arithmetic valid; vendor ID missing, confidence=unknown; duplicate key INV-88/vendor unknown cannot be checked; state=needs review; destination write=none.

Inadequate: “Invoice processed successfully.” Reason: The vendor ID and duplicate check are unresolved, no page evidence is shown, and no authorized destination receipt exists.

## Automation handoff

Document Intake Queue can consume document-intake-record/v1 with document/version ID, schema revision, field source spans, validation, reviewer decision, and destination receipt. Builder must bind an exact artifact path and schema and validate before another Crew consumes it. Re-read current source state.

## Boundaries

Do not upload sensitive files to unapproved tools, silently fill missing fields, create duplicate records, or post an extraction without review. Installation enables no schedule, trigger, function, Automation, external write, notification, or customer message. Never copy another Crew’s credentials.
