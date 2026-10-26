package server

// The person's own AgentWorks agent (PLAT-825-99): an ordinary Crew the platform creates once per person and marks
// with role "agentworks". It looks after the person's Goals; the platform keeps each Goal linked, gives it a tab,
// a notes file and a daily check, and wakes that tab when a scheduled run goes wrong.

// agentWorksRole is the product.json role of the person's AgentWorks Crew.
const agentWorksRole = "agentworks"
