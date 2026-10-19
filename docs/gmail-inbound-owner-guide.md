# Your incoming email

Your personal inbox is one address on a Gmail account you have already connected.
Manage it in **My Vault → Your connected accounts → Google**. Choose **Use for
incoming email** on a connected account with Gmail read access. This creates a
random plus-address on that mailbox; it does not create another mailbox or
Google login. Personal Gmail and Gmail-hosted work accounts are supported.

Ask the Builder of each chosen Crew, Code project or workflow to use your inbox
and choose what work it starts. Builder finds the existing account and routes
for you. **Automation → Incoming email** shows the saved configuration and
activity. Account sign-in and access changes live in My Vault. Product
Connections use the existing account with **Use here / Remove here**.

Each email to your personal address starts every trigger you explicitly enable
and approve for it. One trigger does not consume the email before another can
see it. The platform adds no mandatory sender list, subject rule or Gmail label.
You choose what reaches the address using Gmail or forwarding from other apps.
Optional trigger conditions are available when you ask for more routing.

## Approve what email can start

Anyone who learns this address can email it. Approved triggers run with your
tools, accounts and files, and enabled email replies may share results with the
sender. Keep the address private.

After Builder saves a trigger, choose **Review and approve senders** in chat,
or open **Automation → Incoming email**. Review its account, address, target,
actions, optional conditions and reply setting, acknowledge the access, then
choose **Approve incoming email**. Every trigger needs its own approval, even
when you will email it yourself. Typed messages and tool calls cannot approve.
Changes to the address, account, actions, conditions, reply or enabled state
require review again. Revoke approval in the same card to stop that binding.

**New address** in My Vault replaces a leaked address immediately. Old addresses
and their approvals stop starting work. Ask Builder to reconnect each chosen
trigger, then review the new address and saved action before approving it.

Only authenticated mail is accepted: the sender's domain must pass Gmail's
DMARC check, or Gmail must identify it as the connected mailbox's own sent mail.
Ordinary notifications are accepted. Automatic replies, bounces, spam and trash
are excluded. Older mail received before activation does not start work.

## What runs

Crew and Code start a separate chat per trigger, sender and email conversation.
Replies continue that chat. A workflow runs the exact saved route, full workflow
or standalone step chosen in Builder. Email is untrusted content, and cannot
change access, connections, secrets, sharing or routing configuration.

When requested, Builder can add ordered named rules to a trigger. The first
matching enabled rule chooses its saved chat instruction or workflow route.
Common conditions restrict every rule; alternatives within a sender or phrase
list use OR, and different condition groups use AND. A missing sender list adds
no sender restriction. These optional rules do not change which other enabled
triggers receive the email.

If email replies are enabled, the final response goes to the authenticated
sender, never CC or an incoming Reply-To address. The personal receiving address
is put in Reply-To. Sending permission from Google is required for replies.

**Fetch emails** at the bottom asks this product's chat to read and summarize
recent matching emails. It does not replay deliveries or start the saved action.
Recent activity is stored delivery history, and is separate from fetching mail.

## One-time server setup

Google sign-in alone does not configure automatic delivery. Ask Builder to set
up incoming Gmail. An app administrator with the necessary Google Cloud project
permissions reviews a resource plan and completes Google consent personally.
AgentWorks then configures Gmail/Pub/Sub APIs, the receiving topic, subscription
and authentication, without a server environment edit or restart. Builder checks
progress and readiness. Only use the manual setup checklist when needed.

Sign-in apps for both Google and Slack are managed in **My Vault → Sign-in apps**
by an administrator. Personal Slack connections and the Slack bot that exposes a
product are separate: the first acts as you; the second is under the product's
**Use & share**.

Automatic incoming-email UI is hidden on local installations. Account sign-in,
reading and sending remain available there.

A saved configuration and a ready watch do not prove delivery. Send an email
when you are ready to test, then check activity and the resulting chat or run.
For a rejected email, check the current address, enabled state, approval,
optional conditions, account read access and server delivery setup.
