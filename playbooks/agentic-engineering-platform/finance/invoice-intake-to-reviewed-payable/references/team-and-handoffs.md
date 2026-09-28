# Invoice intake and payables handoff

## Customer journey

1. **Discover:** Builder inspects existing Crew IDs, permissions, invoice document version, required schema, AP policy and current vendor/bill records. It proposes reuse, creation or one combined Crew according to the owner's access boundary.
2. **Extract:** Document Intake Assistant reads the authorized document and emits document-intake-record/v1 with immutable hash/version, exact fields, page references, arithmetic check, identity gaps and review state. The document's printed vendor name is joined to an AP vendor ID with a cited vendor record; a logo or OCR guess does not establish that ID.
3. **Validate:** Run the package validator on the extraction. Stop on missing required fields, unsupported source spans, wrong totals or contradictory duplicate keys. Validation does not establish that a bill is unpaid; Payables must read the live AP source.
4. **Review:** Spend & Payables Coordinator re-reads vendor, bill, credit, approval and payment state for the exact entity and invoice key. It emits payable-review/v1 with the same document identity, existing bill link or reviewed new-bill proposal, policy result, owner decision and next action.
5. **Act separately:** Creating or updating a bill requires exact owner approval, current duplicate check, an idempotency key and destination receipt. Payment requires a separate payment authority, amount/currency/payee verification and provider receipt. Setup does neither.
6. **Verify:** Retain the document and bill IDs, decision, approval and provider receipt. Later re-read AP and payment state; never infer “paid” from bill approval or a scheduled payment.

## Join and safety rules

The artifacts must match legal entity, document ID, hash, version, vendor ID, invoice number, currency and total. The review must cite the extraction, current AP source and the same invoice key. A possible duplicate remains needs-review; an existing or paid bill cannot be proposed as a new bill. Every extracted field needs a page/span citation. A manual case records source coverage and owner corrections before an event or schedule is considered.

## Example acceptance

The fictional examples model an invoice with arithmetic verified and no current matching bill. The payable review is ready for owner review and has no bill write or payment receipt. The invalid example claims paid without a provider payment receipt and fails validation. These examples teach the contract and do not complete customer setup.
