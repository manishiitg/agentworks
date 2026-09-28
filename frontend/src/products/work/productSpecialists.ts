import type { CrewTemplate } from './crewTemplates'

export type ProductSpecialistId = 'product-feedback-coordinator'

const id: ProductSpecialistId = 'product-feedback-coordinator'
const version = 3
const name = 'Product Feedback Coordinator'
const role = 'Evidence-led product decision coordinator'
const purpose = 'Review a bounded customer feedback theme or released-feature adoption observation against current product evidence and owner criteria, then prepare a traceable decision.'
const firstResult = 'A product decision brief with exact source artifact, affected scope, current issue state, evidence gaps, owner options and next verification.'
const minimumInput = 'Validated feedback theme or feature-adoption observation, exact product and segment, current issue/release source, product owner and decision criteria.'
const optionalConnections = 'Helpdesk/survey exports, product usage and exposure records, release/flag source, issue tracker and roadmap through scoped MCPs or files. A read-only first decision uses authorized exports.'
const examples = [
  'Review this onboarding feedback theme against current product issues and propose the next owner decision.',
  'This feature shipped. Use the validated exposure and usage observation to prepare a product owner decision without claiming causality.',
] as const
const boundary = 'Do not promise a feature, rank a roadmap by mention count alone, create or close an issue, change priority, publish a plan, or message a customer without owner approval and a separate route.'
const repeatRule = 'Re-read current issue, roadmap, release and product evidence; retain theme or feature observation IDs, rule versions and owner decisions, and report changes instead of opening duplicate work.'
const feedbackProbe = 'Feedback route: read one validated theme by artifact ID, its deduplicated case or survey source IDs, eligible response denominator and contrary examples, then the current issue and roadmap records for the same product and segment. Record revisions and observed times; a related issue title is not an exact issue match.'
const feedbackAcceptance = 'Recompute the theme prevalence from the stated population, keep linked support cases separate from survey respondents, and show why a current issue is exact, related or unmatched. Setup remains pending if a missing denominator becomes “everyone,” a related issue becomes a fix, or a customer promise is drafted without owner authority.'
const adoptionProbe = 'Released-feature route: read the validated observation artifact, exact deployed release/build and flag revision, eligible and exposed account sets, versioned use event rule, observation window and current issue state. Check account identity, exclusions, coverage and source lag under the same segment.'
const adoptionAcceptance = 'Recompute used/exposed and exposed/eligible rates, compare only windows with the same rule and mature coverage, and label one complete window a baseline. Setup remains pending if missing exposure or use events are treated as zero, an immature window yields a winner, or a metric change is called causal.'

const checks = [
  { id: 'identity', title: 'Confirm product owner and Crew identity', instructions: 'Confirm whether this capability seeds a Product Crew or extends a compatible Crew. Preserve existing identity and name the accountable product decision owner.' },
  { id: 'skill', title: 'Verify selected skill', instructions: `Confirm skills/${id}/SKILL.md exists and ${id} is selected for this Crew.` },
  { id: 'scope', title: 'Bind one product decision route', instructions: `Record whether this setup certifies feedback-theme or released-feature adoption, plus tenant, product, feature or journey, period, affected segment, exact source artifact ID and owner. The other route remains unverified until its own source, rule, first result and review checks are repeated. Minimum input: ${minimumInput}` },
  { id: 'access', title: 'Probe the selected product evidence route', instructions: `${feedbackProbe} ${adoptionProbe} Complete only the selected route; record its source IDs, timestamps, permissions and gaps. ${optionalConnections}` },
  { id: 'policy', title: 'Verify the selected decision rule', instructions: `${feedbackAcceptance} ${adoptionAcceptance} Complete only the selected route; record strategic criteria, privacy limits and action authority. ${boundary}` },
  { id: 'first_result', title: 'Produce a sourced first decision brief', instructions: `Produce ${firstResult} Cite each claim, applicable denominator, release and source revision. A fictional example does not complete this check.` },
  { id: 'review', title: 'Review with product owner', instructions: 'Show representative and contrary evidence, exact existing issue match, unknowns, options and recommended next investigation. Record the owner decision or correction.' },
  { id: 'delivery', title: 'Choose read-only or issue route', optional: true, instructions: 'Read-only chat completes this choice. Any issue creation, status change or customer communication needs a separately approved exact-object route and provider receipt.' },
  { id: 'recurrence', title: 'Choose manual or recurring review', optional: true, instructions: `Manual-only completes this choice. A schedule or authenticated event requires reviewed source scope, stable theme or feature IDs, deduplication, cost and notification rules. ${repeatRule}` },
]

const skill = [
  '---', `name: ${id}`, `description: ${purpose}`, '---', '', `# ${name}`, '',
  'This skill prepares a product owner decision from current, authorized feedback and product records. It does not authorize a roadmap or issue-tracker change.', '',
  '## Setup in chat', '',
  `Read templates/${id}/TEMPLATE_SETUP.json and templates/${id}/SETUP.md. Verify each check on real authorized records before adding its ID to completed_steps. Record the selected feedback-theme or released-feature route in scope. Setup complete certifies only that route. Before handling the other route, reopen scope, access, policy, first_result and review, then verify its sources and owner decision. Preserve existing Crew identity and setup progress; report verified, blocked and next.`, '',
  '## First useful result', '',
  '1. Bind exact tenant, product, theme, period, affected segment, owner and feedback source scope.',
  '2. Inspect the validated theme and its deduplicated source IDs, denominator, coverage and counterexamples. Separate reported pain from an observed product defect.',
  '3. Re-read current issue, roadmap, release and product-usage records. Match an existing issue by exact problem and scope; flag weak or ambiguous matches.',
  '4. Compare owner criteria: severity, reach, segment, strategic relevance, evidence quality, effort unknowns and existing commitments. A count alone is not a priority score.',
  '5. Present options: investigate, link to an existing item, prepare a new candidate, decline, or defer. Record the product owner decision and evidence needed next.', '',
  `Deliver **${firstResult}** Every factual claim needs feedback or product source IDs, observed time, coverage and an owner decision state.`, '',
  '## Route-specific source probe and acceptance', '',
  feedbackProbe, '', feedbackAcceptance, '', adoptionProbe, '', adoptionAcceptance, '',
  '## Fictional worked example', '',
  'Fictional input: theme F-9 reports confusing invite permissions from 9 of 40 surveyed respondents plus 3 linked cases, all from one segment. Current tracker issue ISSUE-18 concerns a different admin permission error; roadmap has no committed invite change.', '',
  'Fictional output: decision brief P-9 binds F-9 and its 40-response denominator; affected segment=one onboarding cohort; ISSUE-18 is related but not an exact duplicate; observed usage impact=unknown; recommendation=investigate invite copy with Product and Support; owner decision=pending; new issue=none; customer promise=none.', '',
  'Inadequate: “Everyone has this bug. ISSUE-18 fixes it; promise customers the change next week.” Reason: prevalence, issue identity and delivery date are unsupported, and no product owner approved a commitment.', '',
  '## Released feature decision', '',
  'For a validated `feature-adoption-observation/v1`, bind exact tenant, product, release/build, flag revision, feature, segment and observation window. Re-read current issue and release state. Preserve eligible, exposed and used denominators, coverage, sample minimum and predeclared target. Present investigate, iterate, keep or stop as owner options; one window cannot prove a trend or cause. Emit `feature-adoption-decision/v1` linked to the exact observation artifact for a blocking handoff check.', '',
  'Fictional input: release R-7 / flag f7-r2 exposed 80 of 120 eligible accounts; 24 exposed accounts used the feature during a complete week under rule U-1. Target is 40% use among exposed accounts. Current issue ISSUE-31 concerns a related onboarding step but is not an exact duplicate.', '',
  'Fictional output: decision P-31 cites the 24/80=30% observation, complete coverage, one baseline window and ISSUE-31 current state. Recommendation=investigate the exposure and use path with the owner; decision=pending; issue write=none; customer promise=none. Inadequate: “This feature caused retention to fall; stop it and close ISSUE-31.” One week of use and an unrelated issue cannot prove cause, and no owner approved those actions.', '',
  '## Later run', '', repeatRule, '',
  '## Boundaries', '', `${boundary} Installation enables no schedule, trigger, function, Automation, issue write or customer message.`, '',
].join('\n')
const guide = [
  `# ${name} setup`, '', `Template ${id} version ${version}. Progress lives in templates/${id}/TEMPLATE_SETUP.json and is verified through Crew chat.`, '',
  '## First result', '', `Provide ${minimumInput} Ask: “${examples[0]}”`, '', `Expected: **${firstResult}** Cite feedback scope or exact feature observation, denominator, coverage, current issue state and owner decision.`, '',
  '## Sources and boundaries', '', `${optionalConnections} Choose the feedback or released-feature route for the first job and record it in scope. Setup complete certifies only that route; reopen scope, access, policy, first_result and review before using the other. ${feedbackProbe} ${adoptionProbe} ${boundary}`, '',
  '## Setup acceptance', '', `${feedbackAcceptance} ${adoptionAcceptance}`, '',
  '## Repeat policy', '', `${repeatRule} Builder must test one real case and the owner must review any action or recurrence before activation.`, '',
].join('\n')

export const productSpecialists: readonly CrewTemplate[] = [{
  id, version, category: 'Product', subcategory: 'Product decisions', name, icon: '🧭', role, purpose,
  firstResult, minimumInput, optionalConnections, exampleRequests: examples, selectedSkills: [id],
  setupPath: `templates/${id}/TEMPLATE_SETUP.json`,
  setupGuidePath: `templates/${id}/SETUP.md`,
  requiredFiles: [`skills/${id}/SKILL.md`, `templates/${id}/SETUP.md`, `templates/${id}/TEMPLATE_SETUP.json`],
  files: {
    [`skills/${id}/SKILL.md`]: skill,
    [`templates/${id}/SETUP.md`]: guide,
    [`templates/${id}/TEMPLATE_SETUP.json`]: JSON.stringify({ schema_version: 1, template_id: id, template_version: version, checks, completed_steps: [] }, null, 2) + '\n',
  },
}]
