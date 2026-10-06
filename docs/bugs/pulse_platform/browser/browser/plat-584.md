[← browser / browser](index.md)

# PLAT-584: Direct CDP recording handoff still expects a fresh tab with the current CLI recorder

| Field | Value |
|---|---|
| State | open |
| Priority | P2 |
| Product | browser |
| Area | browser |
| Summary | Direct-CDP record start expects a distinct new tab, but agent-browser 0.38.2 records the existing selected page. |

## Evidence

Found while implementing extension recording (PLAT-587), by reading the installed
0.38.2 CLI help and upstream version-matched native actions.rs/recording.rs.
Current record start captures the selected page as-is. The direct-CDP executor
still calls findCDPRecordingTab, which accepts only a new/different active tN;
with an unchanged tab set it stops recording and returns
CDP_RECORDING_HANDOFF_FAILED. The full native-CDP reproduction was not run.
The extension path added in PLAT-587 deliberately does not use this handoff.

## Left

Qualify direct-CDP record start/navigation/stop with the installed CLI and align
the owner/selection/artifact lifecycle with its existing-tab recorder. Preserve
cross-workflow exclusive recording and output grants; support historical
context-creating versions only when actually qualified. Update native recording
guidance/context handoff tests alongside that change.
