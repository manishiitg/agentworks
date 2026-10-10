# Global QA Report
**Date**: 2026-03-18

## Executive Summary
- **Overall Pass Rate**: 76.4%
- **Total Tests Run**: 76
- **Total Passed**: 55
- **Total Failed**: 17
- **Role-Skips**: 4
- **Critical/High Bugs**: 3

## Test Environment
- **App URL**: https://staging.customer.example
- **Credentials**: manish@customer.example (Super Admin)
- **Test Date**: 2026-03-18
- **Pages Tested**: genome

## Test Results by Feature
| Page | Pass Rate | Status | Report |
|---|---:|---|---|
| genome | 76.4% | FAIL | [qa_report_genome.md](knowledgebase/qa_report_genome.md) |

## Test Results by Category
| Category | Executed | Pass | Fail | Skip | Role-Skip |
|---|---:|---:|---:|---:|---:|
| Functional | 29 | 23 | 6 | 0 | 0 |
| Error Handling | 11 | 4 | 5 | 0 | 2 |
| Security | 15 | 8 | 5 | 0 | 2 |
| Performance | 4 | 4 | 0 | 0 | 0 |
| Accessibility | 8 | 7 | 1 | 0 | 0 |
| UI/UX | 6 | 6 | 0 | 0 | 0 |
| Compatibility | 3 | 3 | 0 | 0 | 0 |

## Performance Metrics
- Genome page load: 552 ms
- Genome list API: 687 ms
- Genome table render: 687 ms
- Genome save response: 556 ms
- Threshold rating: good(<1s)

## Bugs Found
| Severity | Test Case | Description | Issue Link |
|---|---|---|---|
| medium | TC-GENOME-006 | FAIL: Clicking Contract type did not reorder the rows into alphabetical contract-type order. | https://github.com/server C/server C-v2/issues/2389 |
| high | TC-GENOME-010 | FAIL: Duplicate opened /genomes/019d00ea-9562-7665-8c11-d4db5aa9fc9c with the Name still set to Non-disclosure agreement instead of creating a Copy of genome. | https://github.com/server C/server C-v2/issues/2390 |
| medium | TC-GENOME-011 | FAIL: Clicking CSV on the genome row did not create any file in execution/Downloads. | https://github.com/server C/server C-v2/issues/2391 |
| medium | TC-GENOME-012 | FAIL: Clicking Generate Playbook returned to the list page without any visible success toast or confirmation. | https://github.com/server C/server C-v2/issues/2392 |
| high | TC-GENOME-019 | FAIL: The company was visible in the modal before save, but reopening the modal after Save showed that it did not persist. | https://github.com/server C/server C-v2/issues/2393 |
| low | TC-GENOME-026 | FAIL: Clicking Add Company with no company selected showed `Please select a company` instead of `Company is required`. | https://github.com/server C/server C-v2/issues/2387 |
| medium | TC-GENOME-028 | FAIL: Reloading the page with the genomes endpoint aborted surfaced `Failed to fetch` in the UI instead of the expected `Failed to load genomes` toast. | https://github.com/server C/server C-v2/issues/2394 |
| medium | TC-GENOME-029 | FAIL: Navigating to `/genomes/invalid-uuid` rendered `Failed to fetch` and `Back to Genomes` instead of the expected `Genome not found` message. | https://github.com/server C/server C-v2/issues/2395 |
| low | TC-GENOME-033 | FAIL - `/genomes-superadmin` exposes no search or filter field. Live inspection found 0 `input`, `textarea`, or `[role=searchbox]` elements and no search/filter UI text, so the SQL-injection check could not be exercised on this page. | https://github.com/server C/server C-v2/issues/2396 |
| medium | TC-GENOME-043 | FAIL: Pressing Enter added redantler to the modal list but left the share modal open instead of submitting and closing it. | https://github.com/server C/server C-v2/issues/2397 |
| low | TC-GENOME-051 | FAIL: Could not reach an empty-state tab in the current seeded data; every visible tab contained genomes and No genomes in this category never appeared. | https://github.com/server C/server C-v2/issues/2398 |
| medium | TC-GENOME-052 | FAIL: The Name field accepted 256 characters and retained length 256 instead of truncating to 255 or showing a validation error. | https://github.com/server C/server C-v2/issues/2399 |
| medium | TC-GENOME-061 | FAIL: Unauthenticated `GET /api/review/genomes?view=server C` returned HTTP 401, but the body was `{\"error\":\"Unauthorized - no token provided\"}` instead of `Authentication required`. | https://github.com/server C/server C-v2/issues/2400 |
| medium | TC-GENOME-062 | FAIL: Company-admin `GET /api/review/genomes/:id/share-settings` returned HTTP 403 `{\"error\":\"Forbidden\",\"message\":\"This endpoint requires super-admin privileges\"}` instead of `Only superadmins can access share settings`. | https://github.com/server C/server C-v2/issues/2401 |
| medium | TC-GENOME-063 | FAIL: Company-admin `PATCH /api/review/genomes/:id/share-settings` returned HTTP 403 `{\"error\":\"Forbidden\",\"message\":\"This endpoint requires super-admin privileges\"}` instead of `Only superadmins can update share settings`. | https://github.com/server C/server C-v2/issues/2402 |
| high | TC-GENOME-065 | FAIL: Returned HTTP 200 with `{\"id\":\"019d0081-b7f7-777c-be65-480b786b2df3\",\"genomeId\":\"019d0081-b7f7-777c-be65-480b786b2df3\"}` and accepted the 256-character name instead of rejecting it. | https://github.com/server C/server C-v2/issues/2403 |
| medium | TC-GENOME-068 | FAIL: Company-admin on Microsoft-owned genome `019cfaf4-38a7-777a-b63f-e00097602dd3` (`canEdit=true`, `canDelete=false`, `canShare=false`) was blocked by a generic super-admin 403 before the expected access-escalation validator ran. | https://github.com/server C/server C-v2/issues/2404 |

## Quality Review Summary
- Quality Score: 8/10
- Verdict: pass
- Common issues: sorting, duplicate flow, share persistence, network/404 copy, validation messages, and role-gated API messaging.

## Test Case Coverage
- Total cases: 76
- Category distribution: Functional 29, Error Handling 11, Security 15, Performance 4, Accessibility 8, UI/UX 6, Compatibility 3
- Gaps: SQL-injection coverage lacked a search/filter field; empty-state coverage could not reach a blank tab in current data; six case rows had truncated metadata in the source file.

## Recommendations
1. Fix the failing genome workflows and message copy.
2. Add a searchable/filterable genome surface for security coverage.
3. Seed an empty-state genome dataset for the empty-state test.
4. Normalize the role-specific API error text across GET and PATCH share-settings calls.
