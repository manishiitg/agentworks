# Pulse platform workstreams and ticket index

The active planning surface is the eight workstreams below. Individual PLAT files are evidence and acceptance records, not eight separate backlogs. A ticket’s own state is authoritative; a code merge, deployment, and live verification are different milestones. The [dated register chronology](pulse_platform_issue_register_history_2026-09-29.md) preserves the former long-form register.

## Current workstreams

| Workstream | Ticket categories | Recent anchors | Shared outcome |
|---|---|---|---|
| External calls and human input | human-decisions, integrations | [PLAT-357](pulse_platform/integrations/plat-357.md), [PLAT-365](pulse_platform/human-decisions/plat-365.md), [PLAT-367](pulse_platform/human-decisions/plat-367.md), [PLAT-369](pulse_platform/integrations/plat-369.md), [PLAT-370](pulse_platform/human-decisions/plat-370.md) | Call-scoped questions, replies, live acceptance, and restart continuation. |
| Access, ownership and sandbox | security-sandbox | [PLAT-296](pulse_platform/security-sandbox/plat-296.md), [PLAT-330](pulse_platform/security-sandbox/plat-330.md), [PLAT-339](pulse_platform/security-sandbox/plat-339.md), [PLAT-353](pulse_platform/security-sandbox/plat-353.md), [PLAT-362](pulse_platform/security-sandbox/plat-362.md), [PLAT-364](pulse_platform/security-sandbox/plat-364.md), [PLAT-366](pulse_platform/security-sandbox/plat-366.md) | Owner/editor/run permissions, cross-session isolation, and CLI confinement. |
| Chat and coding-agent continuity | chat-reliability, coding-agent-bridge | [PLAT-178](pulse_platform/chat-reliability/plat-178.md), [PLAT-324](pulse_platform/chat-reliability/plat-324.md), [PLAT-340](pulse_platform/chat-reliability/plat-340.md), [PLAT-351](pulse_platform/coding-agent-bridge/plat-351.md), [PLAT-352](pulse_platform/chat-reliability/plat-352.md), [PLAT-360](pulse_platform/chat-reliability/plat-360.md) | Durable turns, retained sessions, delivery, and restore. |
| Schedules and step execution | scheduler-runs, step-execution | [PLAT-298](pulse_platform/step-execution/plat-298.md), [PLAT-320](pulse_platform/scheduler-runs/plat-320.md), [PLAT-321](pulse_platform/scheduler-runs/plat-321.md), [PLAT-337](pulse_platform/scheduler-runs/plat-337.md), [PLAT-338](pulse_platform/scheduler-runs/plat-338.md), [PLAT-361](pulse_platform/scheduler-runs/plat-361.md), [PLAT-363](pulse_platform/scheduler-runs/plat-363.md) | Occurrence identity, execution ownership, step contracts, and live checks. |
| Builder, plans and knowledge | plans-contracts, learnings-knowledge | [PLAT-329](pulse_platform/plans-contracts/plat-329.md), [PLAT-332](pulse_platform/plans-contracts/plat-332.md), [PLAT-358](pulse_platform/plans-contracts/plat-358.md), [PLAT-359](pulse_platform/plans-contracts/plat-359.md) | Builder operations, plan mutations, contract drift, and guidance. |
| Pulse, evaluation and cost | pulse-governance, evaluation, cost-telemetry | [PLAT-326](pulse_platform/pulse-governance/plat-326.md), [PLAT-333](pulse_platform/evaluation/plat-333.md) | Pulse lifecycle, producer-owned measurement, and accounting. |
| Managed browser | browser-automation | [PLAT-322](pulse_platform/browser-automation/plat-322.md) | Workflow-scoped browser ownership and deployment acceptance. |
| User surface and performance | frontend-chat, performance | [PLAT-342](pulse_platform/performance/plat-342.md), [PLAT-343](pulse_platform/frontend-chat/plat-343.md), [PLAT-344](pulse_platform/performance/plat-344.md), [PLAT-348](pulse_platform/performance/plat-348.md), [PLAT-349](pulse_platform/frontend-chat/plat-349.md) | Chat presentation, visibility, and latency or contention. |

These anchors are a starting point for each workstream, not an exhaustive open-ticket list. Check the linked ticket state and acceptance before claiming completion. Historical and less recent tickets remain available in the complete index below.

## Triage and filing

1. Search the ticket index and workstream before creating a new PLAT ID. Add new evidence or follow-up acceptance to the existing ticket when it has the same root cause and outcome.
2. Create a new PLAT file only for an independently actionable defect with its own owner, fix boundary, and acceptance. Assign it to one category and one workstream.
3. Keep implementation, deployment, and live verification explicit in the ticket. Do not infer closure from a merged PR.
4. Update this workstream table only when the shared outcome or current anchor changes. The complete index must link every ticket file.
5. Resolve a duplicate ID by keeping the substantive ticket and redirecting references to the canonical ID. PLAT-339 is the read-only Crew invocation ticket; the former chat pointer is folded into PLAT-340.

## Complete ticket index

364 unique ticket files across 15 categories as of 2026-09-29. Each link below resolves to the canonical ticket file.

### human-decisions (14)

[PLAT-001](pulse_platform/human-decisions/plat-001.md) · [PLAT-021](pulse_platform/human-decisions/plat-021.md) · [PLAT-042](pulse_platform/human-decisions/plat-042.md) · [PLAT-077](pulse_platform/human-decisions/plat-077.md)
[PLAT-092](pulse_platform/human-decisions/plat-092.md) · [PLAT-093](pulse_platform/human-decisions/plat-093.md) · [PLAT-207](pulse_platform/human-decisions/plat-207.md) · [PLAT-208](pulse_platform/human-decisions/plat-208.md)
[PLAT-218](pulse_platform/human-decisions/plat-218.md) · [PLAT-295](pulse_platform/human-decisions/plat-295.md) · [PLAT-365](pulse_platform/human-decisions/plat-365.md) · [PLAT-367](pulse_platform/human-decisions/plat-367.md)
[PLAT-368](pulse_platform/human-decisions/plat-368.md) · [PLAT-370](pulse_platform/human-decisions/plat-370.md)

### integrations (13)

[PLAT-007](pulse_platform/integrations/plat-007.md) · [PLAT-120](pulse_platform/integrations/plat-120.md) · [PLAT-121](pulse_platform/integrations/plat-121.md) · [PLAT-122](pulse_platform/integrations/plat-122.md)
[PLAT-132](pulse_platform/integrations/plat-132.md) · [PLAT-247](pulse_platform/integrations/plat-247.md) · [PLAT-264](pulse_platform/integrations/plat-264.md) · [PLAT-300](pulse_platform/integrations/plat-300.md)
[PLAT-301](pulse_platform/integrations/plat-301.md) · [PLAT-309](pulse_platform/integrations/plat-309.md) · [PLAT-319](pulse_platform/integrations/plat-319.md) · [PLAT-357](pulse_platform/integrations/plat-357.md)
[PLAT-369](pulse_platform/integrations/plat-369.md)

### security-sandbox (29)

[PLAT-078](pulse_platform/security-sandbox/plat-078.md) · [PLAT-110](pulse_platform/security-sandbox/plat-110.md) · [PLAT-118](pulse_platform/security-sandbox/plat-118.md) · [PLAT-134](pulse_platform/security-sandbox/plat-134.md)
[PLAT-135](pulse_platform/security-sandbox/plat-135.md) · [PLAT-185](pulse_platform/security-sandbox/plat-185.md) · [PLAT-244](pulse_platform/security-sandbox/plat-244.md) · [PLAT-261](pulse_platform/security-sandbox/plat-261.md)
[PLAT-262](pulse_platform/security-sandbox/plat-262.md) · [PLAT-266](pulse_platform/security-sandbox/plat-266.md) · [PLAT-267](pulse_platform/security-sandbox/plat-267.md) · [PLAT-272](pulse_platform/security-sandbox/plat-272.md)
[PLAT-276](pulse_platform/security-sandbox/plat-276.md) · [PLAT-281](pulse_platform/security-sandbox/plat-281.md) · [PLAT-283](pulse_platform/security-sandbox/plat-283.md) · [PLAT-284](pulse_platform/security-sandbox/plat-284.md)
[PLAT-296](pulse_platform/security-sandbox/plat-296.md) · [PLAT-304](pulse_platform/security-sandbox/plat-304.md) · [PLAT-307](pulse_platform/security-sandbox/plat-307.md) · [PLAT-308](pulse_platform/security-sandbox/plat-308.md)
[PLAT-312](pulse_platform/security-sandbox/plat-312.md) · [PLAT-317](pulse_platform/security-sandbox/plat-317.md) · [PLAT-330](pulse_platform/security-sandbox/plat-330.md) · [PLAT-339](pulse_platform/security-sandbox/plat-339.md)
[PLAT-353](pulse_platform/security-sandbox/plat-353.md) · [PLAT-355](pulse_platform/security-sandbox/plat-355.md) · [PLAT-362](pulse_platform/security-sandbox/plat-362.md) · [PLAT-364](pulse_platform/security-sandbox/plat-364.md)
[PLAT-366](pulse_platform/security-sandbox/plat-366.md)

### chat-reliability (6)

[PLAT-178](pulse_platform/chat-reliability/plat-178.md) · [PLAT-323](pulse_platform/chat-reliability/plat-323.md) · [PLAT-324](pulse_platform/chat-reliability/plat-324.md) · [PLAT-340](pulse_platform/chat-reliability/plat-340.md)
[PLAT-352](pulse_platform/chat-reliability/plat-352.md) · [PLAT-360](pulse_platform/chat-reliability/plat-360.md)

### coding-agent-bridge (52)

[PLAT-002](pulse_platform/coding-agent-bridge/plat-002.md) · [PLAT-020](pulse_platform/coding-agent-bridge/plat-020.md) · [PLAT-024](pulse_platform/coding-agent-bridge/plat-024.md) · [PLAT-029](pulse_platform/coding-agent-bridge/plat-029.md)
[PLAT-030](pulse_platform/coding-agent-bridge/plat-030.md) · [PLAT-034](pulse_platform/coding-agent-bridge/plat-034.md) · [PLAT-035](pulse_platform/coding-agent-bridge/plat-035.md) · [PLAT-048](pulse_platform/coding-agent-bridge/plat-048.md)
[PLAT-052](pulse_platform/coding-agent-bridge/plat-052.md) · [PLAT-053](pulse_platform/coding-agent-bridge/plat-053.md) · [PLAT-099](pulse_platform/coding-agent-bridge/plat-099.md) · [PLAT-100](pulse_platform/coding-agent-bridge/plat-100.md)
[PLAT-101](pulse_platform/coding-agent-bridge/plat-101.md) · [PLAT-102](pulse_platform/coding-agent-bridge/plat-102.md) · [PLAT-103](pulse_platform/coding-agent-bridge/plat-103.md) · [PLAT-105](pulse_platform/coding-agent-bridge/plat-105.md)
[PLAT-108](pulse_platform/coding-agent-bridge/plat-108.md) · [PLAT-113](pulse_platform/coding-agent-bridge/plat-113.md) · [PLAT-114](pulse_platform/coding-agent-bridge/plat-114.md) · [PLAT-116](pulse_platform/coding-agent-bridge/plat-116.md)
[PLAT-117](pulse_platform/coding-agent-bridge/plat-117.md) · [PLAT-127](pulse_platform/coding-agent-bridge/plat-127.md) · [PLAT-139](pulse_platform/coding-agent-bridge/plat-139.md) · [PLAT-141](pulse_platform/coding-agent-bridge/plat-141.md)
[PLAT-149](pulse_platform/coding-agent-bridge/plat-149.md) · [PLAT-150](pulse_platform/coding-agent-bridge/plat-150.md) · [PLAT-152](pulse_platform/coding-agent-bridge/plat-152.md) · [PLAT-153](pulse_platform/coding-agent-bridge/plat-153.md)
[PLAT-160](pulse_platform/coding-agent-bridge/plat-160.md) · [PLAT-164](pulse_platform/coding-agent-bridge/plat-164.md) · [PLAT-171](pulse_platform/coding-agent-bridge/plat-171.md) · [PLAT-177](pulse_platform/coding-agent-bridge/plat-177.md)
[PLAT-179](pulse_platform/coding-agent-bridge/plat-179.md) · [PLAT-180](pulse_platform/coding-agent-bridge/plat-180.md) · [PLAT-183](pulse_platform/coding-agent-bridge/plat-183.md) · [PLAT-186](pulse_platform/coding-agent-bridge/plat-186.md)
[PLAT-187](pulse_platform/coding-agent-bridge/plat-187.md) · [PLAT-188](pulse_platform/coding-agent-bridge/plat-188.md) · [PLAT-193](pulse_platform/coding-agent-bridge/plat-193.md) · [PLAT-209](pulse_platform/coding-agent-bridge/plat-209.md)
[PLAT-225](pulse_platform/coding-agent-bridge/plat-225.md) · [PLAT-234](pulse_platform/coding-agent-bridge/plat-234.md) · [PLAT-273](pulse_platform/coding-agent-bridge/plat-273.md) · [PLAT-274](pulse_platform/coding-agent-bridge/plat-274.md)
[PLAT-275](pulse_platform/coding-agent-bridge/plat-275.md) · [PLAT-297](pulse_platform/coding-agent-bridge/plat-297.md) · [PLAT-313](pulse_platform/coding-agent-bridge/plat-313.md) · [PLAT-314](pulse_platform/coding-agent-bridge/plat-314.md)
[PLAT-334](pulse_platform/coding-agent-bridge/plat-334.md) · [PLAT-346](pulse_platform/coding-agent-bridge/plat-346.md) · [PLAT-350](pulse_platform/coding-agent-bridge/plat-350.md) · [PLAT-351](pulse_platform/coding-agent-bridge/plat-351.md)

### scheduler-runs (33)

[PLAT-004](pulse_platform/scheduler-runs/plat-004.md) · [PLAT-017](pulse_platform/scheduler-runs/plat-017.md) · [PLAT-040](pulse_platform/scheduler-runs/plat-040.md) · [PLAT-041](pulse_platform/scheduler-runs/plat-041.md)
[PLAT-047](pulse_platform/scheduler-runs/plat-047.md) · [PLAT-054](pulse_platform/scheduler-runs/plat-054.md) · [PLAT-067](pulse_platform/scheduler-runs/plat-067.md) · [PLAT-070](pulse_platform/scheduler-runs/plat-070.md)
[PLAT-071](pulse_platform/scheduler-runs/plat-071.md) · [PLAT-080](pulse_platform/scheduler-runs/plat-080.md) · [PLAT-086](pulse_platform/scheduler-runs/plat-086.md) · [PLAT-089](pulse_platform/scheduler-runs/plat-089.md)
[PLAT-095](pulse_platform/scheduler-runs/plat-095.md) · [PLAT-097](pulse_platform/scheduler-runs/plat-097.md) · [PLAT-130](pulse_platform/scheduler-runs/plat-130.md) · [PLAT-144](pulse_platform/scheduler-runs/plat-144.md)
[PLAT-145](pulse_platform/scheduler-runs/plat-145.md) · [PLAT-146](pulse_platform/scheduler-runs/plat-146.md) · [PLAT-165](pulse_platform/scheduler-runs/plat-165.md) · [PLAT-176](pulse_platform/scheduler-runs/plat-176.md)
[PLAT-182](pulse_platform/scheduler-runs/plat-182.md) · [PLAT-191](pulse_platform/scheduler-runs/plat-191.md) · [PLAT-194](pulse_platform/scheduler-runs/plat-194.md) · [PLAT-210](pulse_platform/scheduler-runs/plat-210.md)
[PLAT-219](pulse_platform/scheduler-runs/plat-219.md) · [PLAT-241](pulse_platform/scheduler-runs/plat-241.md) · [PLAT-242](pulse_platform/scheduler-runs/plat-242.md) · [PLAT-320](pulse_platform/scheduler-runs/plat-320.md)
[PLAT-321](pulse_platform/scheduler-runs/plat-321.md) · [PLAT-337](pulse_platform/scheduler-runs/plat-337.md) · [PLAT-338](pulse_platform/scheduler-runs/plat-338.md) · [PLAT-361](pulse_platform/scheduler-runs/plat-361.md)
[PLAT-363](pulse_platform/scheduler-runs/plat-363.md)

### step-execution (43)

[PLAT-003](pulse_platform/step-execution/plat-003.md) · [PLAT-005](pulse_platform/step-execution/plat-005.md) · [PLAT-006](pulse_platform/step-execution/plat-006.md) · [PLAT-013](pulse_platform/step-execution/plat-013.md)
[PLAT-022](pulse_platform/step-execution/plat-022.md) · [PLAT-023](pulse_platform/step-execution/plat-023.md) · [PLAT-025](pulse_platform/step-execution/plat-025.md) · [PLAT-027](pulse_platform/step-execution/plat-027.md)
[PLAT-043](pulse_platform/step-execution/plat-043.md) · [PLAT-060](pulse_platform/step-execution/plat-060.md) · [PLAT-061](pulse_platform/step-execution/plat-061.md) · [PLAT-062](pulse_platform/step-execution/plat-062.md)
[PLAT-066](pulse_platform/step-execution/plat-066.md) · [PLAT-082](pulse_platform/step-execution/plat-082.md) · [PLAT-087](pulse_platform/step-execution/plat-087.md) · [PLAT-123](pulse_platform/step-execution/plat-123.md)
[PLAT-125](pulse_platform/step-execution/plat-125.md) · [PLAT-126](pulse_platform/step-execution/plat-126.md) · [PLAT-151](pulse_platform/step-execution/plat-151.md) · [PLAT-162](pulse_platform/step-execution/plat-162.md)
[PLAT-170](pulse_platform/step-execution/plat-170.md) · [PLAT-172](pulse_platform/step-execution/plat-172.md) · [PLAT-174](pulse_platform/step-execution/plat-174.md) · [PLAT-175](pulse_platform/step-execution/plat-175.md)
[PLAT-192](pulse_platform/step-execution/plat-192.md) · [PLAT-195](pulse_platform/step-execution/plat-195.md) · [PLAT-201](pulse_platform/step-execution/plat-201.md) · [PLAT-202](pulse_platform/step-execution/plat-202.md)
[PLAT-211](pulse_platform/step-execution/plat-211.md) · [PLAT-216](pulse_platform/step-execution/plat-216.md) · [PLAT-221](pulse_platform/step-execution/plat-221.md) · [PLAT-238](pulse_platform/step-execution/plat-238.md)
[PLAT-259](pulse_platform/step-execution/plat-259.md) · [PLAT-269](pulse_platform/step-execution/plat-269.md) · [PLAT-280](pulse_platform/step-execution/plat-280.md) · [PLAT-286](pulse_platform/step-execution/plat-286.md)
[PLAT-287](pulse_platform/step-execution/plat-287.md) · [PLAT-288](pulse_platform/step-execution/plat-288.md) · [PLAT-294](pulse_platform/step-execution/plat-294.md) · [PLAT-298](pulse_platform/step-execution/plat-298.md)
[PLAT-328](pulse_platform/step-execution/plat-328.md) · [PLAT-331](pulse_platform/step-execution/plat-331.md) · [PLAT-356](pulse_platform/step-execution/plat-356.md)

### plans-contracts (16)

[PLAT-012](pulse_platform/plans-contracts/plat-012.md) · [PLAT-033](pulse_platform/plans-contracts/plat-033.md) · [PLAT-049](pulse_platform/plans-contracts/plat-049.md) · [PLAT-051](pulse_platform/plans-contracts/plat-051.md)
[PLAT-074](pulse_platform/plans-contracts/plat-074.md) · [PLAT-096](pulse_platform/plans-contracts/plat-096.md) · [PLAT-098](pulse_platform/plans-contracts/plat-098.md) · [PLAT-197](pulse_platform/plans-contracts/plat-197.md)
[PLAT-205](pulse_platform/plans-contracts/plat-205.md) · [PLAT-212](pulse_platform/plans-contracts/plat-212.md) · [PLAT-230](pulse_platform/plans-contracts/plat-230.md) · [PLAT-285](pulse_platform/plans-contracts/plat-285.md)
[PLAT-329](pulse_platform/plans-contracts/plat-329.md) · [PLAT-332](pulse_platform/plans-contracts/plat-332.md) · [PLAT-358](pulse_platform/plans-contracts/plat-358.md) · [PLAT-359](pulse_platform/plans-contracts/plat-359.md)

### learnings-knowledge (21)

[PLAT-037](pulse_platform/learnings-knowledge/plat-037.md) · [PLAT-055](pulse_platform/learnings-knowledge/plat-055.md) · [PLAT-058](pulse_platform/learnings-knowledge/plat-058.md) · [PLAT-059](pulse_platform/learnings-knowledge/plat-059.md)
[PLAT-068](pulse_platform/learnings-knowledge/plat-068.md) · [PLAT-076](pulse_platform/learnings-knowledge/plat-076.md) · [PLAT-128](pulse_platform/learnings-knowledge/plat-128.md) · [PLAT-129](pulse_platform/learnings-knowledge/plat-129.md)
[PLAT-168](pulse_platform/learnings-knowledge/plat-168.md) · [PLAT-173](pulse_platform/learnings-knowledge/plat-173.md) · [PLAT-190](pulse_platform/learnings-knowledge/plat-190.md) · [PLAT-223](pulse_platform/learnings-knowledge/plat-223.md)
[PLAT-228](pulse_platform/learnings-knowledge/plat-228.md) · [PLAT-246](pulse_platform/learnings-knowledge/plat-246.md) · [PLAT-257](pulse_platform/learnings-knowledge/plat-257.md) · [PLAT-263](pulse_platform/learnings-knowledge/plat-263.md)
[PLAT-265](pulse_platform/learnings-knowledge/plat-265.md) · [PLAT-289](pulse_platform/learnings-knowledge/plat-289.md) · [PLAT-310](pulse_platform/learnings-knowledge/plat-310.md) · [PLAT-325](pulse_platform/learnings-knowledge/plat-325.md)
[PLAT-345](pulse_platform/learnings-knowledge/plat-345.md)

### pulse-governance (50)

[PLAT-010](pulse_platform/pulse-governance/plat-010.md) · [PLAT-014](pulse_platform/pulse-governance/plat-014.md) · [PLAT-018](pulse_platform/pulse-governance/plat-018.md) · [PLAT-039](pulse_platform/pulse-governance/plat-039.md)
[PLAT-044](pulse_platform/pulse-governance/plat-044.md) · [PLAT-045](pulse_platform/pulse-governance/plat-045.md) · [PLAT-046](pulse_platform/pulse-governance/plat-046.md) · [PLAT-050](pulse_platform/pulse-governance/plat-050.md)
[PLAT-057](pulse_platform/pulse-governance/plat-057.md) · [PLAT-065](pulse_platform/pulse-governance/plat-065.md) · [PLAT-072](pulse_platform/pulse-governance/plat-072.md) · [PLAT-073](pulse_platform/pulse-governance/plat-073-remaining-board.md)
[PLAT-083](pulse_platform/pulse-governance/plat-083.md) · [PLAT-084](pulse_platform/pulse-governance/plat-084.md) · [PLAT-094](pulse_platform/pulse-governance/plat-094.md) · [PLAT-115](pulse_platform/pulse-governance/plat-115.md)
[PLAT-119](pulse_platform/pulse-governance/plat-119.md) · [PLAT-137](pulse_platform/pulse-governance/plat-137.md) · [PLAT-138](pulse_platform/pulse-governance/plat-138.md) · [PLAT-142](pulse_platform/pulse-governance/plat-142.md)
[PLAT-147](pulse_platform/pulse-governance/plat-147.md) · [PLAT-148](pulse_platform/pulse-governance/plat-148.md) · [PLAT-154](pulse_platform/pulse-governance/plat-154.md) · [PLAT-155](pulse_platform/pulse-governance/plat-155.md)
[PLAT-156](pulse_platform/pulse-governance/plat-156.md) · [PLAT-158](pulse_platform/pulse-governance/plat-158.md) · [PLAT-163](pulse_platform/pulse-governance/plat-163.md) · [PLAT-196](pulse_platform/pulse-governance/plat-196.md)
[PLAT-198](pulse_platform/pulse-governance/plat-198.md) · [PLAT-199](pulse_platform/pulse-governance/plat-199.md) · [PLAT-206](pulse_platform/pulse-governance/plat-206.md) · [PLAT-213](pulse_platform/pulse-governance/plat-213.md)
[PLAT-214](pulse_platform/pulse-governance/plat-214.md) · [PLAT-217](pulse_platform/pulse-governance/plat-217.md) · [PLAT-220](pulse_platform/pulse-governance/plat-220.md) · [PLAT-222](pulse_platform/pulse-governance/plat-222.md)
[PLAT-239](pulse_platform/pulse-governance/plat-239.md) · [PLAT-240](pulse_platform/pulse-governance/plat-240.md) · [PLAT-245](pulse_platform/pulse-governance/plat-245.md) · [PLAT-258](pulse_platform/pulse-governance/plat-258.md)
[PLAT-260](pulse_platform/pulse-governance/plat-260.md) · [PLAT-270](pulse_platform/pulse-governance/plat-270.md) · [PLAT-271](pulse_platform/pulse-governance/plat-271.md) · [PLAT-291](pulse_platform/pulse-governance/plat-291.md)
[PLAT-303](pulse_platform/pulse-governance/plat-303.md) · [PLAT-305](pulse_platform/pulse-governance/plat-305.md) · [PLAT-306](pulse_platform/pulse-governance/plat-306.md) · [PLAT-311](pulse_platform/pulse-governance/plat-311.md)
[PLAT-315](pulse_platform/pulse-governance/plat-315.md) · [PLAT-326](pulse_platform/pulse-governance/plat-326.md)

### evaluation (16)

[PLAT-015](pulse_platform/evaluation/plat-015.md) · [PLAT-016](pulse_platform/evaluation/plat-016.md) · [PLAT-038](pulse_platform/evaluation/plat-038.md) · [PLAT-056](pulse_platform/evaluation/plat-056.md)
[PLAT-075](pulse_platform/evaluation/plat-075.md) · [PLAT-091](pulse_platform/evaluation/plat-091.md) · [PLAT-133](pulse_platform/evaluation/plat-133.md) · [PLAT-189](pulse_platform/evaluation/plat-189.md)
[PLAT-227](pulse_platform/evaluation/plat-227.md) · [PLAT-229](pulse_platform/evaluation/plat-229.md) · [PLAT-236](pulse_platform/evaluation/plat-236.md) · [PLAT-243](pulse_platform/evaluation/plat-243.md)
[PLAT-255](pulse_platform/evaluation/plat-255.md) · [PLAT-282](pulse_platform/evaluation/plat-282.md) · [PLAT-327](pulse_platform/evaluation/plat-327.md) · [PLAT-333](pulse_platform/evaluation/plat-333.md)

### cost-telemetry (17)

[PLAT-008](pulse_platform/cost-telemetry/plat-008.md) · [PLAT-009](pulse_platform/cost-telemetry/plat-009.md) · [PLAT-011](pulse_platform/cost-telemetry/plat-011.md) · [PLAT-019](pulse_platform/cost-telemetry/plat-019.md)
[PLAT-031](pulse_platform/cost-telemetry/plat-031.md) · [PLAT-032](pulse_platform/cost-telemetry/plat-032.md) · [PLAT-036](pulse_platform/cost-telemetry/plat-036.md) · [PLAT-069](pulse_platform/cost-telemetry/plat-069.md)
[PLAT-081](pulse_platform/cost-telemetry/plat-081.md) · [PLAT-088](pulse_platform/cost-telemetry/plat-088.md) · [PLAT-090](pulse_platform/cost-telemetry/plat-090.md) · [PLAT-136](pulse_platform/cost-telemetry/plat-136.md)
[PLAT-166](pulse_platform/cost-telemetry/plat-166.md) · [PLAT-167](pulse_platform/cost-telemetry/plat-167.md) · [PLAT-184](pulse_platform/cost-telemetry/plat-184.md) · [PLAT-203](pulse_platform/cost-telemetry/plat-203.md)
[PLAT-226](pulse_platform/cost-telemetry/plat-226.md)

### browser-automation (14)

[PLAT-028](pulse_platform/browser-automation/plat-028.md) · [PLAT-124](pulse_platform/browser-automation/plat-124.md) · [PLAT-200](pulse_platform/browser-automation/plat-200.md) · [PLAT-204](pulse_platform/browser-automation/plat-204.md)
[PLAT-215](pulse_platform/browser-automation/plat-215.md) · [PLAT-224](pulse_platform/browser-automation/plat-224.md) · [PLAT-231](pulse_platform/browser-automation/plat-231.md) · [PLAT-232](pulse_platform/browser-automation/plat-232.md)
[PLAT-233](pulse_platform/browser-automation/plat-233.md) · [PLAT-235](pulse_platform/browser-automation/plat-235.md) · [PLAT-237](pulse_platform/browser-automation/plat-237.md) · [PLAT-248](pulse_platform/browser-automation/plat-248.md)
[PLAT-249](pulse_platform/browser-automation/plat-249.md) · [PLAT-322](pulse_platform/browser-automation/plat-322.md)

### frontend-chat (35)

[PLAT-026](pulse_platform/frontend-chat/plat-026.md) · [PLAT-063](pulse_platform/frontend-chat/plat-063.md) · [PLAT-064](pulse_platform/frontend-chat/plat-064.md) · [PLAT-085](pulse_platform/frontend-chat/plat-085.md)
[PLAT-104](pulse_platform/frontend-chat/plat-104.md) · [PLAT-106](pulse_platform/frontend-chat/plat-106.md) · [PLAT-107](pulse_platform/frontend-chat/plat-107.md) · [PLAT-109](pulse_platform/frontend-chat/plat-109.md)
[PLAT-111](pulse_platform/frontend-chat/plat-111.md) · [PLAT-112](pulse_platform/frontend-chat/plat-112.md) · [PLAT-131](pulse_platform/frontend-chat/plat-131.md) · [PLAT-140](pulse_platform/frontend-chat/plat-140.md)
[PLAT-143](pulse_platform/frontend-chat/plat-143.md) · [PLAT-159](pulse_platform/frontend-chat/plat-159.md) · [PLAT-161](pulse_platform/frontend-chat/plat-161.md) · [PLAT-169](pulse_platform/frontend-chat/plat-169.md)
[PLAT-250](pulse_platform/frontend-chat/plat-250.md) · [PLAT-251](pulse_platform/frontend-chat/plat-251.md) · [PLAT-252](pulse_platform/frontend-chat/plat-252.md) · [PLAT-253](pulse_platform/frontend-chat/plat-253.md)
[PLAT-254](pulse_platform/frontend-chat/plat-254.md) · [PLAT-256](pulse_platform/frontend-chat/plat-256.md) · [PLAT-268](pulse_platform/frontend-chat/plat-268.md) · [PLAT-277](pulse_platform/frontend-chat/plat-277.md)
[PLAT-278](pulse_platform/frontend-chat/plat-278.md) · [PLAT-279](pulse_platform/frontend-chat/plat-279.md) · [PLAT-292](pulse_platform/frontend-chat/plat-292.md) · [PLAT-293](pulse_platform/frontend-chat/plat-293.md)
[PLAT-299](pulse_platform/frontend-chat/plat-299.md) · [PLAT-316](pulse_platform/frontend-chat/plat-316.md) · [PLAT-318](pulse_platform/frontend-chat/plat-318.md) · [PLAT-335](pulse_platform/frontend-chat/plat-335.md)
[PLAT-336](pulse_platform/frontend-chat/plat-336.md) · [PLAT-343](pulse_platform/frontend-chat/plat-343.md) · [PLAT-349](pulse_platform/frontend-chat/plat-349.md)

### performance (5)

[PLAT-341](pulse_platform/performance/plat-341.md) · [PLAT-342](pulse_platform/performance/plat-342.md) · [PLAT-344](pulse_platform/performance/plat-344.md) · [PLAT-347](pulse_platform/performance/plat-347.md)
[PLAT-348](pulse_platform/performance/plat-348.md)
