[← platform / frontend-chat](index.md)

# PLAT-461 — Auto-hidden navigation leaves an empty strip

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | app |
| Area | ui |
| Summary | fixed on `main`, not deployed: the hidden rail reserves no layout width or shadow; its invisible edge target reveals the mounted navigation over the workspace. |

| Coordination | Value |
|---|---|
| State | fixed on `main`; not deployed |
| Date | 2026-10-04 |
| Owner | frontend-chat |

## Problem

The screenshot supplied after PLAT-460 shows an empty vertical strip when
navigation is hidden: the shell reserves six pixels and its hidden shadow
remains visible at the workspace edge.

## Fix

Auto-hide reserves no layout width. Its invisible six-pixel reveal target
overlays the workspace; the 48-pixel rail and its shadow appear on edge hover
or keyboard focus. Fixed mode still reserves the full rail width. The live
navigation children stay mounted.

## Validation

- ProductTopBar tests verify zero-width hidden layout, saved preferences,
  hint interaction, and live monitor children staying mounted.
- Browser preview measured hidden wrapper width 0, rail left -48, shadow
  `none`, and workspace left 0; verified hover/keyboard reveal.
- Frontend type checking passed.

## Left

Deployment and verification on deployed product surfaces.

## Register notes

[PLAT-461](plat-461.md), fixed on `main`, not deployed:
the hidden rail reserves no layout width or shadow; its invisible edge target
reveals the mounted navigation over the workspace.
