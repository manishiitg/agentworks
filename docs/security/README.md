# Security

AgentWorks runs AI agents that read files, run commands, and call services on
your behalf. Everything they do passes through five layers:

<div class="sec-flow">
<div class="sec-box"><b>1. Login account</b><span>An admin creates your account. You sign in with SSO or a password — no self sign-up.</span></div>
<div class="sec-arrow">→</div>
<div class="sec-box"><b>2. Ownership</b><span>Your files, chats, and runs are yours. Sharing is explicit and re-checked every time.</span></div>
<div class="sec-arrow">→</div>
<div class="sec-box"><b>3. Your own Linux user</b><span>On shared servers, your work runs as your own system account — others can't reach it.</span></div>
<div class="sec-arrow">→</div>
<div class="sec-box"><b>4. Allowed folders only</b><span>Each task declares which folders it may touch. The OS enforces the list.</span></div>
<div class="sec-arrow">→</div>
<div class="sec-box"><b>5. One login at a time</b><span>AI logins are handed to one run at a time, never left lying around.</span></div>
</div>

## The one thing to understand

| Isolation does | Isolation does not do |
|---|---|
| Keep your work away from other people | Hide a login from the person using it |
| Keep other people's work away from you | Stop you copying a shared login you legitimately hold |
| Keep server secrets out of every agent's reach | Let anyone act as you without signing in |

Your **login account** (how you sign in — admin-created) and your **AI
logins** (Claude, Codex, Cursor — which you connect yourself unless your
admin locked it) are two different things. The first proves who you are; the
second is what your agents spend.

## Go deeper

- [Per-user Linux accounts](per_user_linux_accounts.md) — how people are
  separated on a shared server, including terminals.
- [Provider credentials](provider_credentials.md) — shared vs private AI
  logins, and how they reach one run at a time.
- [Secrets](../core/secrets.md) — the workflow and user secret stores.

<div class="sec-note">Found something that looks wrong? Open an issue on the <a href="https://github.com/manishiitg/agentworks/issues">AgentWorks repository</a> with "security:" in the title, or ask your workspace administrator to pass it to the team. Please don't include passwords, tokens, or customer data.</div>
