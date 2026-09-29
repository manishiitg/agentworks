/**
 * One-line descriptions for the bundled connectors.
 *
 * The MCP tool list carries per-*tool* descriptions but nothing at the server
 * level, so the directory cards would otherwise have an empty second line.
 * These live here for the same reason `brandMarks` does: they are presentation
 * copy, not configuration, and a server missing an entry degrades gracefully.
 *
 * Keyed by the server name exactly as it appears in `mcp_servers_clean.json`.
 * Keep each one short enough to sit on two lines in a card.
 */
const CONNECTOR_DESCRIPTIONS: Record<string, string> = {
  GoogleGmail: 'Search, read, and draft email in Gmail',
  GoogleDrive: 'Search and read files in Google Drive',
  GoogleDocs: 'Read and edit Google Docs',
  GoogleSheets: 'Read and edit Google Sheets',
  GoogleSlides: 'Read and edit Google Slides',
  GoogleCalendar: 'See calendars, events, and free/busy time',
  GoogleChat: 'Read spaces and send Google Chat messages',
  GooglePeople: 'Look up your directory and contacts',
  GitHub: 'Repositories, issues, pull requests, and Actions',
  GitLab: 'Projects, issues, merge requests, and pipelines',
  Slack: 'Search and read channels and DMs, and post messages',
  Neon: 'Serverless Postgres projects, branches, and SQL',
  PrismaPostgres: 'Prisma Postgres databases, schema, and queries',
  PlanetScale: 'Databases, branches, and deploy requests',
  Render: 'Services, deploys, logs, and metrics on Render',
  Railway: 'Projects, deployments, variables, and logs on Railway',
  Buildkite: 'Pipelines, builds, jobs, and logs',
  Semgrep: 'Scan code for security issues and review findings',
  Socket: 'Security and quality scores for dependencies',
  Pulumi: 'Pulumi Cloud stacks, resources, and deployments',
  Heroku: 'Apps, dynos, add-ons, config, and logs',
  Stytch: 'Stytch projects, API keys, and settings',
  HubSpot: 'CRM contacts, companies, deals, and tickets',
  Box: 'Search and read Box files and folders',
  Jam: 'Bug reports with video, console, and network logs',
  ParallelSearch: 'Web search built for agents',
  Wolfram: 'Computation and curated knowledge from Wolfram',
  Cortex: 'Service catalog, ownership, and scorecards',
  Notion: 'Search, update, and create pages across your workspace',
  Linear: 'Manage issues, projects, and team cycles',
  Sentry: 'Investigate errors and performance issues in your projects',
  Canva: 'Search, create, autofill, and export Canva designs',
  Airtable: 'Query and update records across your Airtable bases',
  PostHog: 'Explore product analytics, funnels, and session data',
  Grafana: 'Query dashboards, metrics, and alert rules',
  Honeycomb: 'Query traces and debug production behaviour',
  MongoDB: 'Explore collections and run queries against your clusters',
  Apify: 'Run scrapers and automation actors on the Apify platform',
  WorkOS: 'Manage organizations, users, and SSO connections',
  Resend: 'Send transactional email and inspect delivery logs',
  Paddle: 'Review subscriptions, transactions, and customer billing',
  Port: 'Query your service catalog and developer portal',
  Indeed: 'Search job listings and manage employer postings',
  Lovable: 'Build and deploy apps and websites from a prompt',
  Supabase: 'Manage and query your Postgres databases',
  Vercel: 'Inspect deployments, logs, and project settings',
  Atlassian: 'Work with Jira issues and Confluence pages',
  Asana: 'Track tasks, projects, and team workload',
  Intercom: 'Search conversations, contacts, and help articles',
  ClickUp: 'Manage tasks, docs, and project spaces',
  Mixpanel: 'Query product analytics events and funnels',
  Stripe: 'Review payments, customers, and subscriptions',
  Zapier: 'Trigger and run automations across your apps',
  GitHub: 'Work with repositories, pull requests, and issues',
  HubSpot: 'Search and update CRM records and marketing data',
  Square: 'Manage payments, orders, and catalog items',
  PayPal: 'Review transactions, invoices, and payouts',
  Webflow: 'Manage sites, CMS collections, and publishing',
  'monday.com': 'Track boards, items, and team workflows',
  Netlify: 'Inspect sites, deploys, and build logs',
  Cloudflare: 'Manage Workers, KV, R2, and DNS bindings',
  Datadog: 'Query metrics, monitors, logs, and traces',
  Miro: 'Work with boards, frames, and sticky notes',
  CircleCI: 'Inspect pipelines, workflows, and job logs',
  Loops: 'Send product email and manage contacts',
  Shortcut: 'Track stories, epics, and iterations',
  Context7: 'Fetch up-to-date docs and code examples for libraries',
  DeepWiki: 'Ask questions about any public GitHub repository',
  MicrosoftLearn: 'Search official Microsoft and Azure documentation',
  AWSKnowledge: 'Search AWS documentation, APIs, and best practices',
  Svelte: 'Look up Svelte and SvelteKit docs and migrations',
  CloudflareDocs: 'Search Cloudflare product documentation',
  Exa: 'Neural web search with full page contents',
  Browserbase: 'Drive a headless browser to navigate and extract data',
  Clerk: 'Manage users, sessions, and authentication settings',
  LlamaCloud: 'Query documents indexed in LlamaCloud',
  Chainstack: 'Query blockchain nodes and network data',
  Nslookup: 'Run DNS lookups and inspect records',
  Sonatype: 'Check dependency versions, licences, and vulnerabilities',
  HuggingFace: 'Search models, datasets, and spaces, and run inference',
  Postman: 'Work with API collections, environments, and mocks',
  Bitrise: 'Trigger mobile CI builds and inspect logs',
  BrightData: 'Collect and unblock web data at scale',
  Unstructured: 'Turn PDFs and documents into structured data',
  Todoist: 'Manage tasks, projects, and due dates',
  Dropbox: 'Search, read, and organise files in your Dropbox',
  Lucid: 'Work with diagrams and whiteboards',
  Zendesk: 'Search tickets, users, and help centre articles',
  Plain: 'Manage support threads and customer timelines',
  Typeform: 'Build forms and read survey responses',
  Figma: 'Read designs, components, and design tokens',
  OpusClip: 'Turn long videos into short vertical clips',
}

/**
 * Copy for a connector card's second line. Custom servers a user adds
 * themselves have no entry, so they fall back to a neutral label rather than
 * leaving the card looking unfinished.
 */
export function descriptionFor(serverName: string): string {
  return CONNECTOR_DESCRIPTIONS[serverName] ?? 'Custom MCP server'
}

/**
 * Job-based shelf a connector belongs to in the directory's unconnected
 * list: fine-grained business shelves first (payments, customers, marketing,
 * ...), power tools next, developer tools last. Presentation copy like the
 * descriptions above, not configuration.
 *
 * Keyed by the server name exactly as it appears in `mcp_servers_clean.json`.
 * A server with no entry (including user-added custom servers, which only
 * ever arrive through the technical JSON path) falls into `developer`, so
 * nothing drops out of the directory for want of a group. Shelves with no
 * matches (e.g. `accounting` until QuickBooks lands) simply don't render.
 */
export type ConnectorGroup =
  | 'payments'
  | 'accounting'
  | 'customers'
  | 'marketing'
  | 'social'
  | 'seo'
  | 'gtm'
  | 'releases'
  | 'storefront'
  | 'productivity'
  | 'hiring'
  | 'search'
  | 'data'
  | 'automation'
  | 'advanced'
  | 'developer'

const CONNECTOR_GROUPS: Record<string, ConnectorGroup> = {
  // Workspace, CRM and data servers added 2026-09-29 (the rest are developer tools).
  GoogleGmail: 'productivity',
  GoogleDrive: 'productivity',
  GoogleDocs: 'productivity',
  GoogleSheets: 'productivity',
  GoogleSlides: 'productivity',
  GoogleCalendar: 'productivity',
  GoogleChat: 'productivity',
  GooglePeople: 'productivity',
  Slack: 'productivity',
  Box: 'productivity',
  HubSpot: 'customers',
  ParallelSearch: 'search',
  Wolfram: 'search',
  Neon: 'data',
  PrismaPostgres: 'data',
  PlanetScale: 'data',
  // Payments: taking money.
  Stripe: 'payments',
  Square: 'payments',
  PayPal: 'payments',
  // Accounting: intentionally empty until QuickBooks/Xero land.
  // Customer data and support.
  HubSpot: 'customers',
  Intercom: 'customers',
  Zendesk: 'customers',
  Plain: 'customers',
  // Marketing: design, video, lead forms (Mailchimp/Google Business later).
  Canva: 'marketing',
  Figma: 'marketing',
  OpusClip: 'marketing',
  Typeform: 'marketing',
  // Social networks: empty until LinkedIn/X/YouTube land.
  // SEO: empty until Search Console/Ahrefs/AEO trackers land.
  // GTM: empty until Clay/Apollo/Instantly land.
  // Releases: empty until Product Hunt/Beamer/Canny land.
  // Sell online: sites today, Shopify tomorrow.
  Webflow: 'storefront',
  // Productivity: tasks, docs, files, whiteboards.
  Notion: 'productivity',
  Asana: 'productivity',
  ClickUp: 'productivity',
  'monday.com': 'productivity',
  Todoist: 'productivity',
  Dropbox: 'productivity',
  Atlassian: 'productivity',
  Miro: 'productivity',
  Lucid: 'productivity',
  // Hiring: job posts today, payroll (Gusto) tomorrow.
  Indeed: 'hiring',
  // Search: look things up (Tavily and Firecrawl later).
  Exa: 'search',
  // Data: structured data stores.
  Airtable: 'data',
  // Automation: the glue.
  Zapier: 'automation',
  // Advanced tools: situational power tools (scraping, raw email APIs,
  // product analytics, dev billing, AI builders).
  Apify: 'advanced',
  BrightData: 'advanced',
  Browserbase: 'advanced',
  Unstructured: 'advanced',
  Loops: 'advanced',
  Resend: 'advanced',
  Mixpanel: 'advanced',
  PostHog: 'advanced',
  Paddle: 'advanced',
  Lovable: 'advanced',
  // Developer tools: infra, observability, CI, auth, docs, AI plumbing.
  Sentry: 'developer',
  Datadog: 'developer',
  Grafana: 'developer',
  Honeycomb: 'developer',
  Cloudflare: 'developer',
  CloudflareDocs: 'developer',
  Vercel: 'developer',
  Netlify: 'developer',
  Supabase: 'developer',
  MongoDB: 'developer',
  CircleCI: 'developer',
  Bitrise: 'developer',
  Postman: 'developer',
  Sonatype: 'developer',
  Port: 'developer',
  WorkOS: 'developer',
  Clerk: 'developer',
  Chainstack: 'developer',
  Nslookup: 'developer',
  Linear: 'developer',
  Shortcut: 'developer',
  Context7: 'developer',
  DeepWiki: 'developer',
  MicrosoftLearn: 'developer',
  AWSKnowledge: 'developer',
  Svelte: 'developer',
  HuggingFace: 'developer',
  LlamaCloud: 'developer',
}

/** Display order and labels for the directory's shelves. */
export const GROUP_ORDER: { id: ConnectorGroup; label: string }[] = [
  { id: 'payments', label: 'Payments' },
  { id: 'accounting', label: 'Accounting' },
  { id: 'customers', label: 'Customers' },
  { id: 'marketing', label: 'Marketing' },
  { id: 'social', label: 'Social networks' },
  { id: 'seo', label: 'SEO' },
  { id: 'gtm', label: 'GTM' },
  { id: 'releases', label: 'Releases' },
  { id: 'storefront', label: 'Sell online' },
  { id: 'productivity', label: 'Productivity' },
  { id: 'hiring', label: 'Hiring & HR' },
  { id: 'search', label: 'Search' },
  { id: 'data', label: 'Data' },
  { id: 'automation', label: 'Automation' },
  { id: 'advanced', label: 'Advanced tools' },
  { id: 'developer', label: 'Developer tools' },
]

/**
 * GROUP_ORDER for a coding workspace (Code): developer, data, release and
 * productivity shelves first, then the business ones.
 */
export const DEVELOPER_FIRST_GROUP_ORDER: { id: ConnectorGroup; label: string }[] = [
  ...(['developer', 'data', 'releases', 'productivity', 'search', 'advanced', 'automation'] as ConnectorGroup[])
    .map(id => GROUP_ORDER.find(group => group.id === id)!),
  ...GROUP_ORDER.filter(group => !['developer', 'data', 'releases', 'productivity', 'search', 'advanced', 'automation'].includes(group.id)),
]

/** The shelf a connector belongs to. */
export function groupFor(serverName: string): ConnectorGroup {
  return CONNECTOR_GROUPS[serverName] ?? 'developer'
}
