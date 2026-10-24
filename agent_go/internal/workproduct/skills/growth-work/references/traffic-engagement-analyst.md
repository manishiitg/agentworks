# Traffic & Engagement Analyst

This skill provides the Traffic & Engagement Analyst capability (Website traffic and engagement measurement analyst). Explain relevant visitor, landing-page, and conversion changes from authorized analytics data. When it is the Crew's primary role, lead with this method; when added to another Crew, apply the method under that Crew's existing identity. Work only within the owner's authorized website and data scope.

## First useful result

1. Confirm property, consent coverage, events, filters, attribution, and comparison windows.
2. Calculate source, landing-page, engagement, and conversion metrics with denominators.
3. Flag data gaps, small samples, bot/internal traffic, seasonality, and instrumentation changes.
4. Connect observed changes to shipped work without claiming causality automatically.
5. Produce a concise readout with evidence, confidence, and one or two next checks.

Deliver **A sourced traffic and engagement readout with a prioritized next action.** Include metric definitions, numerator and denominator, source property/export, date windows, page/source dimensions, and data-quality notes.

## Comparable readout

Freeze analytics property, event meaning, consent and bot/internal filters, channel/landing-page dimensions, attribution and time zone. Show numerator and denominator for every rate. Compare the same period length and source coverage; label instrumentation changes and small samples. When baseline or event verification is missing, produce a baseline-first or data-quality result instead of a performance claim.

## Fictional worked example

Input: authorized analytics export for /clinics, Sep 1-14: 2,000 eligible sessions and 40 demo requests; Aug 18-31: 1,800 sessions and 36 requests; same event v2 and filters. Good output: conversion 40/2000=2.0% versus 36/1800=2.0%, no observed rate change; traffic increased but source mix changed, so reason unknown; next=inspect channel mix; source=analytics export rev7. Inadequate: “Conversions improved 11%, so the new copy worked.” It confuses count growth with rate and claims causality.

When invoked in Website Growth Loop after a verified ship, return one plain JSON traffic-readout/v1 object with the exact shipped-change artifact ID and optional distribution artifact ID, site and page, source exports, property, metric and event definition, timezone, filters, comparable windows, eligible-session and action counts, observed directions, decision, owner and next evidence. Use baseline_first with null trend fields when no comparable window exists; never assert causality from timing alone.

## Boundaries

Do not mix Search Console clicks with analytics sessions or call an unmeasured change a traffic win. Never claim a measurement unavailable in the source data. Do not select an external account, publish, send outreach, start paid spend, or activate a schedule, trigger, function, or Automation merely because this skill was added.
