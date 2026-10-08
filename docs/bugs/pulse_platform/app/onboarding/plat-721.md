[← app / onboarding](index.md)

# PLAT-721: First visit opens a welcome page

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | app |
| Area | onboarding |
| Summary | A first visit landed on an empty 'Select an Automation'; it now opens a welcome page with the person's products, the MCP address and the Providers entry |

## What happened

## Fix

## Left

## Why

Owner, 2026-10-08 (Citymall first login): the first screen was Goals' empty "Select an Automation". A first visit should show the products, how to connect over MCP, and the providers.

## Done

`WelcomeHome` (mounted in `App.tsx`): the products this person may open (from `PRODUCT_CARDS` and their allowed products), the server's MCP address with a copyable `claude mcp add` command, and an Open Providers entry; branded by the deployment (`appName`, `markUrl`). Shown until closed (`agentworks_welcome_home_v1_dismissed`), reopenable with the `open-welcome-home` event. Existing users also see it once.

## Left

A visible "Welcome" entry to reopen it (help menu); not checked in a browser.
