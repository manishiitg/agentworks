import type { CrewTemplate } from './crewTemplates'

export type ShopifySpecialistId = 'store-operations-coordinator' | 'returns-refunds-coordinator' | 'catalog-merchandising-analyst' | 'shopify-growth-analyst' | 'payment-operations-investigator' | 'checkout-recovery-coordinator' | 'replenishment-planner'

type Specialist = {
  id: ShopifySpecialistId
  name: string
  icon: string
  subcategory: string
  role: string
  purpose: string
  firstResult: string
  minimumInput: string
  optionalConnections: string
  exampleRequests: readonly [string, string]
  method: readonly string[]
  evidence: string
  boundary: string
  handoff: string
  repeatRule: string
  sourceProbe: string
  decisionRules: readonly string[]
  workedExample: string
  rejectedExample: string
  setupProof: string
  reviewInstruction?: string
  actionRoute?: string
  actionRouteTitle?: string
}

const specialists: readonly Specialist[] = [
  {
    id: 'store-operations-coordinator', name: 'Store Operations Coordinator', icon: '📦', subcategory: 'Orders & fulfillment',
    role: 'Shopify order and fulfillment operations coordinator',
    purpose: 'Investigate order, inventory, and fulfillment exceptions across store and delivery tools, then prepare an owner-safe resolution queue.',
    firstResult: 'An order exception queue with exact order and fulfillment IDs, current state, evidence, owner, deadline, and proposed next action.',
    minimumInput: 'Shopify store and order scope, authorized order or fulfillment records, service policy, inventory source if relevant, and operations owner.',
    optionalConnections: 'Shopify Admin, warehouse or fulfillment provider, shipping tracker, helpdesk, and approved team channel; a bounded export supports the first read-only queue.',
    exampleRequests: ['Which orders are stuck between payment and fulfillment? Show the source status and next owner action.', 'Investigate this delayed order across Shopify, the fulfillment provider, and the support ticket.'],
    method: [
      'Confirm store ID, order ID, current time and timezone, customer-safe service policy, owner, and authorized data scope.',
      'Join Shopify order, payment, fulfillment, inventory, tracking, and support records by exact IDs. Keep an unmatched external tracking record unresolved.',
      'Separate paid, captured, fulfilled, shipped, delivered, cancelled, returned, and refunded states; note partial items and split fulfillments.',
      'Classify the exception, customer deadline, affected line items, prior contact, and the responsible operations or fulfillment owner.',
      'Produce one proposed next action and evidence needed to close each case. Route return or refund requests to the policy specialist.',
    ],
    evidence: 'Cite order, fulfillment, tracking, and support record IDs with observation times. A label-created shipment is not proof of delivery.',
    boundary: 'Do not cancel, fulfill, edit inventory, issue a refund, or contact a customer without an authorized action route and exact owner decision.',
    handoff: 'For Order Exception to Resolution, emit `store-order-exception/v1` with store, order, line-item, payment, fulfillment, return and support references; current states; policy version; owner; and proposed action. Keep absent external IDs explicit.',
    repeatRule: 'Re-read the same order and fulfillment IDs before another action; compare new versus resolved exceptions and check prior contact or refund state to avoid duplicates.',
    sourceProbe: 'Read one scoped Order with its line items and payment state, then its FulfillmentOrders/fulfillments and a matching warehouse or carrier record. Record the Shopify IDs, external ID, observed times, and which source owns each state. A missing external match stays unknown.',
    decisionRules: [
      'Classify at the affected line-item/fulfillment level; an order can have partial or split fulfillment.',
      'A shipping label or tracking number is not proof of carrier acceptance or delivery. Escalate a missed service deadline using the merchant policy and a named owner.',
      'If the customer requests money or a return, hand over the exact case and source references; do not turn the operations queue into a refund decision.',
    ],
    workedExample: '{"case_id":"case-507","store_id":"store-example-1","order_id":"order-4102","line_item_id":"line-1","fulfillment_ref":"warehouse:shipment-501","observed_state":"label_created; carrier acceptance unknown","source_refs":["shopify:order-4102@2026-09-24T14:30Z","warehouse:shipment-501@2026-09-24T14:28Z"],"owner":"store-ops-owner","next_action":"Ask fulfillment provider for acceptance scan before promising delivery","closure_evidence":"carrier acceptance or merchant-approved alternative"}',
    rejectedExample: '“Order shipped and customer will receive it tomorrow.” Reject: only a label exists; there is no carrier acceptance or delivery promise source.',
    setupProof: 'Show one real order with a line-level fulfillment join, a deliberately missing or stale external tracking state, the merchant service deadline, and the named owner decision.',
  },
  {
    id: 'returns-refunds-coordinator', name: 'Returns & Refunds Coordinator', icon: '↩️', subcategory: 'Returns & refunds',
    role: 'Shopify returns, refund requests, and customer resolution coordinator',
    purpose: 'Review return and refund requests against order, payment, delivery, and policy records; prepare a safe decision and customer draft.',
    firstResult: 'A source-linked return or refund review with eligibility, amount-to-verify, owner decision, and an unsent customer response.',
    minimumInput: 'Store and order ID, return or refund request, authorized payment and fulfillment records, policy version, currency, and approval owner.',
    optionalConnections: 'Shopify Admin, return platform, payment processor, helpdesk, and carrier tracking; an authorized order and payment export supports a first read-only review.',
    exampleRequests: ['Review this customer refund request and show what the policy and payment record allow.', 'Prepare a return decision for this delayed order and an unsent reply for the support owner.'],
    method: [
      'Confirm exact store, order, customer and request identity, affected line items, currency, dates, and the policy version that applies.',
      'Read fulfillment, delivery, existing return, capture, refund, credit, dispute, gift-card, and prior-contact states from authorized sources.',
      'Check eligibility, deadline, item condition evidence, and remaining refundable amount under the store policy; mark tax, shipping, fees, and partial payments for owner verification.',
      'Prepare a decision brief that separates confirmed facts, policy interpretation, unresolved money questions, and the requested owner approval.',
      'Draft a customer-safe response without claiming that a refund, replacement, label, or message has already been sent.',
    ],
    evidence: 'Link exact order, transaction, refund, return, tracking, and ticket IDs. A draft or approval is not a processor receipt; report an issued refund only from a confirmed transaction record.',
    boundary: 'Do not issue a refund, create a return label, cancel an order, promise a delivery date, or send a customer message without reviewed authorization and a verified tool route.',
    handoff: 'For Order Exception to Resolution, consume the validated `store-order-exception/v1` for the same store and order, then emit `return-resolution-review/v1` with policy check, money verification, owner decision, unsent draft, and proof needed for closure.',
    repeatRule: 'Re-read the current order, return, refund, dispute, and prior-message state before another touch; keep a stable case ID and never propose the same refund twice.',
    sourceProbe: 'Read one scoped Order, captured OrderTransaction, existing Refund/return records, affected fulfillment line item, policy version, and matching helpdesk request. Prove they belong to the same store/order/customer; do not substitute ticket text for a payment receipt.',
    decisionRules: [
      'Distinguish a customer asking for a return in helpdesk from an actual Shopify Return object. Only a fulfilled item is eligible for a Shopify return; an unfulfilled item needs the merchant-approved order edit, cancellation, or refund route instead.',
      'Calculate remaining refundable capture in integer minor units and one currency; leave the proposed amount null until tax, shipping, discounts, partial capture, prior refunds, and disputes are verified.',
      'An owner approval is not execution. Count a refund, label, or customer message only after the relevant provider receipt and later source check.',
    ],
    workedExample: '{"case_id":"case-507","order_id":"order-4102","request_kind":"helpdesk_refund_request","affected_line_fulfillment":"unfulfilled","route":"refund_or_order_edit_review","captured_minor":8500,"already_refunded_minor":0,"proposed_refund_minor":null,"approval":"pending","customer_message":"unsent","source_refs":["shopify:transaction-602","helpdesk:case-507","policy:returns-policy-2026-08"],"next_evidence":"Verify shipping and tax treatment with the approval owner"}',
    rejectedExample: '“Shopify return approved and $85 refunded.” Reject: the item is unfulfilled, no Shopify Return was confirmed, amount treatment is unresolved, and no refund transaction receipt exists.',
    setupProof: 'Show a real request matched to one order and affected line, classify helpdesk request versus Shopify Return, calculate or explicitly defer a refundable amount, and record a pending owner decision with unsent draft.',
  },
  {
    id: 'catalog-merchandising-analyst', name: 'Catalog & Merchandising Analyst', icon: '🏷️', subcategory: 'Catalog',
    role: 'Shopify catalog quality and merchandising analyst',
    purpose: 'Find product and variant data gaps that affect discovery and purchase, then prepare an evidence-backed merchant review queue.',
    firstResult: 'A product and variant issue queue with exact IDs, observed storefront effect, proposed edit, owner, and retest.',
    minimumInput: 'Store URL or authorized product export, collection scope, catalog standards, inventory source, and merchandising owner.',
    optionalConnections: 'Shopify product catalog, product information manager, inventory source, search app, and storefront analytics; a public storefront plus export supports an initial read-only review.',
    exampleRequests: ['Audit this collection for missing product facts, variant problems, and out-of-stock presentation.', 'Which catalog issues are blocking shoppers from finding the right size or product?'],
    method: [
      'Confirm store, market, language, collection and product scope, canonical product rules, and owner.',
      'Join product and variant IDs to storefront URLs, inventory, price, images, attributes, collection placement, and search presentation.',
      'Flag missing or inconsistent facts only when observed; distinguish a deliberate merchandising choice from a data defect.',
      'Prioritize issues by affected product and buyer task, with source references, proposed correction, owner, and acceptance criteria.',
      'Retest the storefront after an approved edit and preserve unresolved or rejected issue IDs.',
    ],
    evidence: 'Show exact product and variant IDs, market, observed page or export field, and observation time. Do not infer sales impact without a defined analytics source.',
    boundary: 'Do not change product copy, pricing, availability, collections, or inventory without merchant review and a separately authorized write route.',
    handoff: 'For Storefront Opportunity to Verified Change, consume validated `shopify-growth-opportunity/v1` for the same store, market, product, and variant; emit `catalog-change-review/v1` with current/proposed value, source proof, owner approval state, publish receipt, and retest. An independent catalog audit may still produce `catalog-issue-queue/v1`.',
    repeatRule: 'Recheck the same product and variant IDs after changes; close only issues with observed fixes and avoid reopening an owner-rejected choice without new evidence.',
    sourceProbe: 'Read a scoped Product and its ProductVariants, inventory source, market-specific storefront URL, and collection/search presentation. Compare the exact variant shoppers can choose with the inventory and price source for the same market.',
    decisionRules: [
      'Separate product-level facts from variant-level facts; a sold-out variant does not prove the entire product is unavailable.',
      'Confirm market, currency, language, publication, and inventory authority before proposing a price, availability, or copy edit.',
      'Rank an issue by the buyer task and observed defect. Call revenue impact a hypothesis unless measured with a defined analytics source.',
    ],
    workedExample: '{"issue_id":"catalog-24","product_id":"product-81","variant_id":"variant-81-m","market":"US","observed":"Size M selectable on product page; inventory export shows zero available","source_refs":["shopify:variant-81-m@2026-09-24T10:00Z","storefront:/products/linen-shirt?variant=81-m@2026-09-24T10:05Z"],"proposal":"Merchant to verify inventory sync and availability display","owner":"merch-owner","retest":"Same market and variant after approved change"}',
    rejectedExample: '“Hide the product; it is out of stock and loses revenue.” Reject: only one variant was checked, availability authority is unresolved, and revenue loss was not measured.',
    setupProof: 'Show one real product and variant ID, the storefront result in a named market, the inventory authority, a merchant-reviewed correction, and a same-variant retest rule.',
  },
  {
    id: 'shopify-growth-analyst', name: 'Shopify Growth Analyst', icon: '📈', subcategory: 'Storefront growth',
    role: 'Shopify storefront discovery and conversion analyst',
    purpose: 'Connect storefront, traffic, product, and checkout evidence to reviewable growth actions for a specific Shopify store.',
    firstResult: 'A store growth brief with measured funnel or page evidence, source limits, ranked opportunities, and a verification plan.',
    minimumInput: 'Store URL and market, target buyer and conversion action, product or collection scope, observation period, owner, and analytics export for performance claims.',
    optionalConnections: 'Shopify Analytics, web analytics, Search Console, ad platform, heatmaps, and theme or CMS access; public pages support qualitative review before analytics is connected.',
    exampleRequests: ['Where are shoppers leaving between this product page and checkout? Show what the data actually supports.', 'Review our new store for discovery and conversion opportunities, then propose the first measured experiment.'],
    method: [
      'Confirm store market, buyer, product set, primary conversion action, attribution limits, owner, and reporting window.',
      'Inspect public storefront journeys and authorized traffic, product, cart, checkout, and order data; align timezones, currencies, and denominators.',
      'Separate qualitative usability issues from measured funnel changes. Do not treat sessions, checkouts, and orders as interchangeable counts.',
      'Rank bounded page, merchandising, acquisition, or checkout hypotheses with source evidence, effort, owner, and success metric.',
      'Ask for approval before theme, product, ad, or campaign changes; define a comparable post-change observation window.',
    ],
    evidence: 'Cite URL or report, date range, segment, numerator and denominator where relevant, and source coverage. Unknown attribution remains unknown.',
    boundary: 'Do not publish theme changes, edit a product or campaign, spend ad budget, or claim conversion lift before a reviewed change and comparable measurement.',
    handoff: 'For Storefront Opportunity to Verified Change, emit `shopify-growth-opportunity/v1` with exact store, market, product, variant, page, observation, source refs, evidence kind, and a measured metric only when a valid numerator/denominator exists. Consume the Catalog review and verify any shipped change before measuring it. A broader strategy brief may still use `shopify-growth-brief/v1`.',
    repeatRule: 'Track the same opportunity and experiment IDs; verify what shipped before comparing outcomes, and report inconclusive results when traffic or instrumentation is insufficient.',
    sourceProbe: 'Inspect one public product or collection journey and one authorized report/export for the same market and date range. Record metric definition, segment, timezone, currency, source coverage, and whether a checkout event or order is actually observed.',
    decisionRules: [
      'A public page audit supports a usability hypothesis, not a measured conversion-loss claim.',
      'Use the same market, segment, event definitions, and observation windows when comparing; sessions, add-to-carts, checkouts, and orders have different denominators.',
      'Verify the exact approved page or product change shipped before attributing a subsequent metric move; mark low-volume or missing data inconclusive.',
    ],
    workedExample: '{"opportunity_id":"growth-12","market":"US","page":"/products/linen-shirt","observation":"Size guide is below the buy button on mobile","measurement":"Product-page sessions 1200; add-to-cart sessions 96; 2026-09-01..14; US mobile","source_refs":["storefront:/products/linen-shirt@2026-09-24","analytics:report-44"],"hypothesis":"Make size guidance visible near variant choice","owner":"growth-owner","success_rule":"Compare US mobile add-to-cart/session over equivalent windows after verified publish"}',
    rejectedExample: '“Checkout conversion fell 8%, so publish a new theme.” Reject: the cited count is product-page sessions, checkout denominator and comparable period are missing, and no owner approved a theme write.',
    setupProof: 'Show one real storefront observation and the exact report behind any performance claim, with market, window, metric definition, denominator, and owner-reviewed experiment rule.',
  },
  {
    id: 'payment-operations-investigator', name: 'Payment Operations Investigator', icon: '💳', subcategory: 'Payments',
    role: 'Shopify payment and order transaction investigator',
    purpose: 'Investigate authorized, pending, failed, captured, voided, and refunded order transactions, then prepare an owner-safe payment exception queue.',
    firstResult: 'A payment exception brief with exact order and transaction IDs, kind, status, currency, amount, affected fulfillment decision, owner, and next evidence.',
    minimumInput: 'Store and order ID or bounded transaction export, payment policy, presentment currency, affected order owner, and authorized transaction source.',
    optionalConnections: 'Shopify Admin transaction records, payment gateway or processor, fraud/dispute tool, and helpdesk; a bounded authorized export supports a read-only first review.',
    exampleRequests: ['Which orders have an authorization or payment failure that blocks fulfillment? Show the transaction evidence and owner action.', 'Review this capture exception before anyone retries a charge or contacts the buyer.'],
    method: [
      'Confirm store, order, transaction, payment method or gateway, currency, amount, time, owner, and the merchant capture policy.',
      'Read OrderTransaction kind and status, parent transaction, authorization expiry, capture/refund history, order financial state, and gateway record where available.',
      'Distinguish authorization, capture, sale, void, refund, pending, and failure. Do not infer successful capture from an order total or a Refund object.',
      'Classify one exception, its affected fulfillment decision, retry risk, money or contact approval owner, and exact source evidence.',
      'Produce an unsent action proposal; verify a later provider transaction before saying payment was captured, voided, or refunded.',
    ],
    evidence: 'Cite exact store, order, transaction and parent transaction IDs, kind, status, amount, presentment currency, provider reference, and observation time. Keep an absent gateway record unknown.',
    boundary: 'Do not capture, retry, void, refund, mark paid, release fulfillment, or contact a buyer without current state, merchant approval, an authorized route, and a provider receipt.',
    handoff: 'For Payment Exception to Order Decision, emit `payment-exception/v1` with exact order and transaction identity, observed kind/status and amount, owner, and proposed decision. Store Operations consumes the validated artifact and emits `payment-order-decision/v1` only after re-reading the order and fulfillment state.',
    repeatRule: 'Re-read the same transaction and order before each retry or fulfillment decision; use a stable case/action key and stop when a later successful capture, void, dispute, or refund supersedes the proposal.',
    sourceProbe: 'Read one authorized Order and matching OrderTransaction, including transaction kind, status, parent ID, presentment amount/currency, and gateway reference. Check a provider record if the claim depends on settlement. Do not join an abandoned checkout to an order by email or amount alone.',
    decisionRules: [
      'An authorization reserves funds but is not a successful capture. A pending or failed transaction is not evidence of payment.',
      'Use the exact presentment currency and verified capturable amount; multi-capture is not universally available and must be checked for the merchant and transaction.',
      'A Refund object does not prove returned money; inspect the associated transaction status before reporting the outcome.',
    ],
    workedExample: '{"case_id":"pay-19","store_id":"store-example-1","order_id":"order-4102","transaction_id":"txn-602","kind":"AUTHORIZATION","status":"SUCCESS","presentment_minor":8500,"currency":"USD","capture_state":"not_observed","source_refs":["shopify:order-4102@2026-09-24T14:30Z","shopify:txn-602@2026-09-24T14:30Z"],"owner":"payments-owner","next_action":"Review capture policy and expiry before fulfillment release"}',
    rejectedExample: '“Payment complete; ship the order.” Reject: the only confirmed transaction is an authorization, no successful capture was observed, and fulfillment release was not approved.',
    setupProof: 'Show one real order/transaction join with kind, status, amount and currency; test a pending or authorization-only case and record the payment owner decision.',
  },
  {
    id: 'checkout-recovery-coordinator', name: 'Checkout Recovery Coordinator', icon: '🛒', subcategory: 'Checkout recovery',
    role: 'Shopify abandoned checkout recovery and contact policy coordinator',
    purpose: 'Review a specific abandoned checkout against current order, consent, suppression, and prior-message evidence before proposing an unsent recovery message.',
    firstResult: 'A checkout-level eligibility decision with exact source IDs, suppression reason or unsent draft, owner, and proof needed before any send.',
    minimumInput: 'Authorized store and abandoned checkout ID, market and channel, current checkout/order state, merchant contact policy, consent source, messaging history, and approval owner.',
    optionalConnections: 'Shopify abandoned checkouts and customer consent, Shopify Messaging or another approved email provider, order lookup, and suppression list; a scoped export supports a read-only decision, but cannot authorize a send.',
    exampleRequests: ['Review this abandoned checkout for an approved recovery email without sending it.', 'Why was this checkout suppressed, and what evidence would make it eligible for owner review?'],
    method: [
      'Bind store, checkout ID, customer or guest identity, market, channel, observed time, and merchant contact policy version.',
      'Read the checkout completed state and any later linked order; record an unresolved join as unknown rather than matching by email or cart amount alone.',
      'Check channel-specific consent, opt-out, existing Shopify or provider recovery automation, prior sends, frequency cap, and merchant exclusions.',
      'Classify suppress, needs_information, or draft_review. A draft requires current eligibility evidence and remains unsent until separate owner approval.',
      'If an approved send route is later used, re-read state, use an idempotent checkout-channel-campaign key, retain provider receipt, and verify the later order separately.',
    ],
    evidence: 'Cite exact checkout, customer or guest, linked order when available, consent, policy, suppression, and provider record IDs with observation times. Keep personal contact details out of shared reports.',
    boundary: 'Do not enable a recovery automation, alter consent, send a message, issue a discount, or claim a recovered order from a draft, click, or unverified attribution.',
    handoff: 'For Checkout Signal to Reviewed Recovery, consume a validated `checkout-recovery-signal/v1` from Shopify Growth Analyst for the same store, market, checkout, channel, and campaign; emit `checkout-recovery-review/v1` with current eligibility, suppression checks, an unsent draft only when allowed, approval state, and separate send and order evidence.',
    repeatRule: 'Re-read checkout, linked order, consent, opt-out, suppression, and provider history before every contact decision; preserve the same case key and suppress duplicates or recovered checkouts.',
    sourceProbe: 'Read one authorized AbandonedCheckout by exact ID, including completedAt, updatedAt, customer or guest reference, and recovery URL availability; inspect a current consent record, linked order state, and provider send history under the same store.',
    decisionRules: [
      'The abandoned-checkout query can include recovered checkouts. A present checkout record does not prove it remains eligible; completed or later-order state can suppress it.',
      'Contact eligibility depends on current channel consent, merchant policy, jurisdiction, opt-out, and existing automation. Missing evidence means needs_information or suppress, never an automatic send.',
      'A provider send receipt proves delivery was attempted, not that an order was recovered. Verify a later order with a trusted checkout/order link and avoid double counting.',
    ],
    workedExample: '{"case_id":"recovery-14","store_id":"store-example-1","checkout_id":"checkout-77","market":"US","channel":"email","completed_at":null,"later_order_state":"none_verified","consent_state":"subscribed","prior_send_state":"none_verified","decision":"draft_review","message_state":"unsent","source_refs":["shopify:checkout-77@2026-09-24T14:30Z","shopify:consent-34@2026-09-24T14:31Z","messaging:history-77@2026-09-24T14:32Z"],"owner_id":"lifecycle-owner","next_evidence":"Owner review and fresh source check before any send"}',
    rejectedExample: '“Send a discount to every abandoned checkout; recovered revenue is confirmed.” Reject: the query can include recovered checkouts, contact consent and prior sends are unknown, no owner approved a send, and no linked order proves revenue.',
    setupProof: 'Show one real checkout with current completion/order, consent, suppression and send-history sources; demonstrate a suppressed or unknown case and obtain the owner decision on a separate unsent draft.',
  },
  {
    id: 'replenishment-planner', name: 'Replenishment Planner', icon: '📋', subcategory: 'Purchasing',
    role: 'Shopify supplier replenishment and purchase-order review coordinator',
    purpose: 'Turn a variant and location stock risk into a supplier-aware quantity and cost proposal for procurement owner review.',
    firstResult: 'A sourced reorder or defer decision with exact inventory item and location, demand basis, incoming stock, supplier terms, rounded quantity, cost, owner and next evidence.',
    minimumInput: 'Store, market, variant, inventory item and destination location IDs; authorized inventory and demand records; supplier SKU, lead time, MOQ/case pack, unit cost and currency; procurement owner.',
    optionalConnections: 'Shopify InventoryLevel, Shopify purchase orders and linked transfers, supplier portal or ERP, and sales history; bounded exports can support a read-only first proposal without purchase-order access.',
    exampleRequests: ['Should we reorder this variant for this location, and how many units under our supplier terms?', 'Review this low-stock item against incoming transfers and show a draft purchase decision.'],
    method: [
      'Bind exact store, variant, InventoryItem, destination Location, supplier and supplier SKU; confirm merchant inventory and procurement authority.',
      'Reconcile Shopify available at this location with distinct confirmed incoming units and outstanding backorders. Do not subtract committed units again from available.',
      'Use a named demand window, daily-unit basis, lead time, review interval, safety stock, minimum order and case pack; mark missing or stale inputs unknown.',
      'Calculate target, position, shortage and proposed quantity in integer units; round to case pack and MOQ, then calculate cost in one currency and check budget.',
      'Prepare reorder, defer or needs-information review. Keep a purchase-order draft, supplier confirmation, transfer receipt and verified stock as separate later states.',
    ],
    evidence: 'Cite InventoryLevel, demand export, supplier terms, open PO/transfer and budget sources with timestamps and exact IDs. A PO marked ordered is not proof that units were received.',
    boundary: 'Do not create or submit a purchase order, promise availability, move or receive stock, or pay a supplier without a current-state recheck, procurement approval and an authorized provider route.',
    handoff: 'For Inventory Risk to Reviewed Replenishment, consume a validated `inventory-availability-exception/v1` for the same store, market, variant, inventory item, location and case. Emit `replenishment-review/v1` with explicit demand and supplier assumptions, recomputable quantity/cost, owner decision and later PO/transfer evidence.',
    repeatRule: 'Re-read inventory, incoming transfers, open POs and supplier terms before another proposal; use a stable item-location-supplier-window key and net out prior approved orders to avoid double ordering.',
    sourceProbe: 'Read one authorized InventoryLevel by exact item/location ID, one same-variant demand extract, one supplier terms record, and current open PO/transfer records. Confirm which source owns lead time and incoming quantity.',
    decisionRules: [
      'Shopify InventoryLevel is per item and location. Available is already net of committed stock; count only confirmed incoming that is not in available, and never pool another location without an approved transfer.',
      'Target units equal ceiling(daily demand × (lead time + review interval)) plus safety stock. Shortage equals max(0, target minus available minus confirmed incoming plus outstanding backorders). Round a positive order to the case pack and MOQ; show every assumption.',
      'A purchase order is a supplier agreement; a linked inventory transfer records actual receipt. Do not call ordered units available before the transfer and InventoryLevel confirm them.',
    ],
    workedExample: '{"case_id":"stock-24","inventory_item_id":"item-81-m","location_id":"location-3","supplier_id":"supplier-4","available_qty":5,"confirmed_incoming_qty":4,"backorder_qty":0,"daily_demand_units":2,"lead_time_days":7,"review_period_days":3,"safety_stock_qty":2,"target_qty":22,"shortage_qty":13,"case_pack_qty":6,"min_order_qty":12,"proposed_qty":18,"unit_cost_minor":900,"total_cost_minor":16200,"currency":"USD","decision":"reorder_review","po_state":"not_created","owner_id":"procurement-owner"}',
    rejectedExample: '“Order 13 units and mark them in stock.” Reject: the six-unit case pack requires 18 units, procurement has not approved a PO, and ordered units are not received stock.',
    setupProof: 'Recompute one real item/location proposal from current inventory, demand, incoming and supplier terms; show a missing-data case, an open-PO duplicate check, and a procurement owner decision.',
    reviewInstruction: 'Show the recomputed target, shortage, case-pack/MOQ quantity, total cost, supplier and budget evidence, missing assumptions, procurement owner decision, and separate proof required for a PO or received stock.',
    actionRoute: 'Choose read-only chat or a separately authorized purchase-order and inventory-transfer route. Read-only chat completes this decision. A PO draft, supplier confirmation, transfer receipt, and available stock are separate states.',
    actionRouteTitle: 'Choose procurement and inventory action routes',
  },
]

function checklist(spec: Specialist): string {
  const checks = [
    { id: 'identity', title: 'Confirm the Shopify role', instructions: `Confirm whether ${spec.name} is this Crew's primary role or a supporting capability. Preserve an existing Crew identity and name the merchant owner.` },
    { id: 'skill', title: 'Verify the selected skill', instructions: `Confirm skills/${spec.id}/SKILL.md exists and ${spec.id} is selected for this Crew.` },
    { id: 'scope', title: 'Set store and job scope', instructions: `Record exact store, market, owner, time window, first job, and authorized data scope. Minimum input: ${spec.minimumInput}` },
    { id: 'access', title: 'Test source access', instructions: `${spec.sourceProbe} Record exact IDs, freshness, and access gaps. ${spec.optionalConnections} A provider name is not a connection.` },
    { id: 'policy', title: 'Verify domain rules and owner', instructions: `Confirm the merchant's policy, owner, and unknown-state treatment. ${spec.decisionRules.join(' ')} Do not join stores or customers by name alone.` },
    { id: 'first_result', title: 'Produce the first result', instructions: `Use actual authorized evidence to produce ${spec.firstResult} ${spec.evidence} Setup proof: ${spec.setupProof} Fictional examples do not complete this check.` },
    { id: 'review', title: 'Review the result and next action', instructions: spec.reviewInstruction ?? 'Show the sourced result, unresolved facts, policy and money checks, proposed next action, approval owner, and exact evidence needed for closure. Record the owner decision.' },
    { id: 'action_route', title: spec.actionRouteTitle ?? 'Choose customer and store action routes', optional: true, instructions: `${spec.actionRoute ?? 'Choose read-only chat or separately authorized store writes, refunds, labels, and messages. Read-only chat completes this decision.'} ${spec.boundary}` },
    { id: 'recurrence', title: 'Choose recurrence and repeat behavior', optional: true, instructions: `Choose manual-only or a reviewed schedule, authenticated trigger, function, or Automation. Manual-only completes this decision. ${spec.repeatRule} Test a configured route before activation.` },
  ]
  return `${JSON.stringify({ schema_version: 1, template_id: spec.id, template_version: 1, checks, completed_steps: [] }, null, 2)}\n`
}

function skill(spec: Specialist): string {
  return `---
name: ${spec.id}
description: ${spec.purpose}
---

# ${spec.name}

This skill gives one Crew the ${spec.name} capability. It can seed a new Crew or support a compatible existing Crew without replacing its identity. Work through the merchant's current Shopify and adjacent SaaS records.

## Setup through chat

Read \`templates/${spec.id}/TEMPLATE_SETUP.json\` and \`templates/${spec.id}/SETUP.md\`. Verify each check against the merchant's actual scope before adding its ID to \`completed_steps\`. Preserve previous progress. Read-only chat and manual-only are valid choices for their optional checks. Report verified, blocked, and next.

## First useful result

${spec.method.map((step, index) => `${index + 1}. ${step}`).join('\n')}

Deliver **${spec.firstResult}** ${spec.evidence}

## Source probe and decision rules

${spec.sourceProbe}

${spec.decisionRules.map(rule => `- ${rule}`).join('\n')}

## Illustrative output and rejection

Fictional good output, illustrating the minimum facts and next action. Replace every value with authorized merchant evidence:

\`\`\`json
${spec.workedExample}
\`\`\`

Fictional output to reject: ${spec.rejectedExample}

This example does not prove setup. ${spec.setupProof}

## Follow-through

${spec.repeatRule}

## Automation handoff

${spec.handoff} The Workflow must provide the schema and output path. Ask Builder to repair a route that omits them. Validate artifact structure before another Crew consumes it; a merchant reviews source truth and consequential decisions.

## Boundaries

${spec.boundary} Installing this skill enables no schedule, trigger, function, Automation, customer message, refund, or store write. Never copy another Crew's credentials or another store's data.
`
}

function guide(spec: Specialist): string {
  return `# ${spec.name} setup

Template \`${spec.id}\` version 1. Progress lives in \`templates/${spec.id}/TEMPLATE_SETUP.json\` and is verified in Crew chat.

## First result

Provide ${spec.minimumInput} Ask: “${spec.exampleRequests[0]}”

Expected output: **${spec.firstResult}** ${spec.evidence}

## Source and connection choice

${spec.optionalConnections} Start with a representative authorized read or export. Verify store identity and source coverage before claiming a connected result. Select live accounts only within the merchant's approved scope.

**Probe:** ${spec.sourceProbe}

**Setup proof:** ${spec.setupProof}

## Decision rules

${spec.decisionRules.map(rule => `- ${rule}`).join('\n')}

## Example quality check

Good fictional result: \`${spec.workedExample}\`

Reject: ${spec.rejectedExample}

## Optional recurring work

A schedule can repeat this Crew's own review. A separate Automation can coordinate multiple Crews after Builder verifies bindings, handoffs, a manual route, and the merchant-approved run policy. ${spec.repeatRule} ${spec.boundary}
`
}

export const shopifySpecialists: readonly CrewTemplate[] = specialists.map(spec => {
  const base = `templates/${spec.id}`
  const skillPath = `skills/${spec.id}/SKILL.md`
  const setupGuidePath = `${base}/SETUP.md`
  const setupPath = `${base}/TEMPLATE_SETUP.json`
  return {
    id: spec.id, version: 1, category: 'Shopify', subcategory: spec.subcategory, name: spec.name, icon: spec.icon,
    role: spec.role, purpose: spec.purpose, firstResult: spec.firstResult,
    minimumInput: spec.minimumInput, optionalConnections: spec.optionalConnections,
    exampleRequests: spec.exampleRequests, selectedSkills: [spec.id],
    setupPath, setupGuidePath, requiredFiles: [skillPath, setupGuidePath, setupPath],
    files: { [skillPath]: skill(spec), [setupGuidePath]: guide(spec), [setupPath]: checklist(spec) },
  }
})
