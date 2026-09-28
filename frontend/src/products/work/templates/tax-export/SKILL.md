---
name: tax-export
description: Prepare traceable transaction exports and exception lists for review by a business and its tax professional.
---

# Tax Export Preparer

Use this skill when the owner wants to organize authorized records for a tax handoff. This skill can coexist with Finance Analyst in one Crew. Keep the Crew's identity and other template files intact.

## Setup

Read `templates/tax-export/TEMPLATE_SETUP.json` and `templates/tax-export/SETUP.md`. Work through pending checks in chat. Add a check ID to `completed_steps` only after verifying its instructions. Preserve the checklist and earlier progress. An explicit decision to skip an optional connection or delivery route completes that optional check.

## Export

1. Confirm period, jurisdiction, currency, source files, and the recipient's required format. Ask for missing details before producing a final export.
2. Build a transaction table with stable source references, dates, amounts, currency, category, and any tax fields present in the source. Keep original values and any transformations traceable.
3. Check duplicates, missing records, refunds, reversals, mixed currencies, and category ambiguity. Reconcile totals to source summaries when possible.
4. Produce an export plus a separate exceptions list and a short handoff note stating sources, filters, assumptions, and unresolved questions.

Do not invent tax codes, determine final tax liability, file a return, alter accounting records, or send data to a third party without owner authorization. Route uncertain classification to the owner or their tax professional.

## Fictional export and reconciliation

Input: one legal entity, September 2026, USD. Authorized processor export contains customer payment P-101 of 100 for invoice I-101, refund R-101 of 20 linked to P-101, fee F-101 of 3, and payout PO-101 of 77. Bank statement shows deposit D-101 of 77 linked to PO-101. The owner and professional have not supplied tax-category rules. The format below is a review sample, not a tax filing.

```csv
source_id,date,source_type,linked_id,original_amount,currency,normalized_signed_amount,classification_status,tax_code,source_ref
P-101,2026-09-10,customer_payment,I-101,100.00,USD,100.00,pending_review,,processor:P-101
R-101,2026-09-12,customer_refund,P-101,20.00,USD,-20.00,pending_review,,processor:R-101
F-101,2026-09-12,processor_fee,P-101,3.00,USD,-3.00,pending_review,,processor:F-101
D-101,2026-09-15,bank_transfer,PO-101,77.00,USD,77.00,non_income_transfer,,bank:D-101
```

Reconciliation note: processor cash movement is 100 − 20 − 3 = 77, matching PO-101 and bank deposit D-101. D-101 is the transfer of already counted proceeds; adding it to customer receipts would double count. The source refund and fee amounts were positive in the export and are shown negative only in the normalized signed column. The exception list asks the owner or professional to confirm recipient columns, entity, tax basis, classification and treatment of refunds/fees; a missing tax field stays blank rather than zero. Verify the source period and whether any late adjustments exist before calling the export complete.

Inadequate: “Taxable sales are 177 USD and all rows have tax code A.” It double counts the bank transfer and invents classifications. On a later run, retain source and destination IDs, re-read changed records and prior exceptions, deduplicate by source ID and version, and issue a revision with a reconciliation delta rather than silently replacing the reviewed file.
