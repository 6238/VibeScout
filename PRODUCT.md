# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users

Primary users are FRC Team 6238's own field scouts, pit scouts, and strategy/drive team — nobody outside the team. Field scouts capture match observations (three-robot alliance mode, or single-robot focused mode) on a phone or tablet at the field, live during matches. Pit scouts interview other teams in the pits, usually before or between matches. The strategy team reviews the resulting AI analysis (pick list, next-match strategy) between matches to make alliance-selection and in-match decisions, often under real time pressure.

## Product Purpose

VibeScout turns raw scouting observations — match notes, pit interviews, official match data, video, and EPA — into strategic analysis the drive/strategy team can actually trust and act on quickly: a pick list, per-match strategy, and reliability signals. It exists because confident-sounding AI analysis that isn't backed by enough data is worse than no analysis at all. Success means the team's real strategic decisions (who to pick, how to play a match) are better-informed, not led astray by a verdict that sounds sure of itself when it shouldn't be.

## Positioning

Unlike a scouting app that just collects data and hands it to an LLM for a confident summary, VibeScout is built around AI analysis exposing its own uncertainty rather than hiding it: verdicts are suppressed outright below a low-data threshold, a notes-based verdict that diverges sharply from Statbotics EPA is flagged rather than presented flat, pit interviews (self-reported) count for less than what scouts actually observed in matches, disagreement between scouts covering the same match is surfaced explicitly instead of silently blended, and a breakdown match counts as reliability evidence rather than dragging down a robot's scoring rating. When the AI still gets something wrong, anyone can attach a "clarification" directly to the misread note — appended, never rewriting the original scout's words — without hunting through raw data to fix it. Offline reliability is treated the same way: scouting data saves to the device before it ever touches the network.

## Operating Context

Used live at real FRC competition events (confirmed: including Chezy Champs), across qualification and playoff matches over multiple days. Field and pit scouting happen in the stands or in the pits, often on unreliable venue wifi. The strategy side runs the Pick List, Next Match, and Match Planner views between matches to decide strategy quickly. A dedicated test event (`2026test`) exists for rehearsing the whole flow outside of a real competition.

## Capabilities and Constraints

- Field scouting: three-robot (alliance) and one-robot (focused) modes; a structured checklist (broke / played defense / was defended / auto type) plus free-text notes; voice dictation with a review step; offline-first save-then-sync so a scout's work survives a dropped connection.
- Pit scouting: one structured interview per team, editable in place, not scoped to any single event (a team's pit note persists across events).
- AI analysis (Gemini): a per-team verdict with scoring/reliability/defense breakdown, a pick list, and per-match strategy plans. Suppressed below a low-data threshold rather than shown low-confidence; flags disagreement against Statbotics EPA; keeps pit (self-reported) evidence visually and analytically separate from field-observed evidence; a match where the robot broke counts toward reliability, not scoring.
- Multi-scout reconciliation: checklist answers OR together across scouts on the same match (a real issue is worse to under-report than over-report); a single-robot-mode scout's account is treated as more reliable than one split across a whole alliance; genuine disagreement is flagged to both humans and the model, never silently resolved.
- Clarifications: anyone can attach a correction to a misread pit or field note (e.g. "2.5 cycles" meaning during auto only, not the whole match); appended after the original note, visible to everyone, deletable by anyone, and immediately invalidates that team's cached analysis so the fix is reflected right away.
- Match video: links to TBA's official match video once posted, otherwise a timestamped moment in the event's YouTube livestream; an admin tool can auto-fill scouting notes for a team by watching match video directly.
- TBA/Statbotics integration: schedules, EPA, rankings, and forecasts, cached with stale-while-revalidate behavior so a bad connection shows last-known data instead of nothing.
- No user authentication exists yet. The admin route is reachable only via an unguessable path, not real access control; scouting submissions and clarifications are anonymous or self-reported by a typed name, never verified.
- Single-team tool by design: the team number (6238) is hardcoded, not a per-deployment setting.

## Brand Commitments

Product name: VibeScout. Belongs to FRC Team 6238; not intended for use by or distribution to other teams.

## Evidence on Hand

Confirmed real: an AI verdict rated team 2910 "Average" at Chezy Champs, and that team went on to captain and win the event — the incident that drove this project's "trustworthy analysis" effort (suppressing overconfident verdicts, flagging notes-vs-EPA disagreement, surfacing scout disagreement, and clarifications). VibeScout is deployed and in real use at competitions (Railway-hosted). No other usage metrics, roster size, or event history are established — future work must not invent them.

## Product Principles

1. An AI verdict must show its own uncertainty (too little data, EPA disagreement) rather than sound confident regardless of what it's actually based on.
2. A self-reported claim (a pit interview) counts for less than what scouts actually observed happen in a match.
3. A real reliability problem — a breakdown, a disagreement between scouts — is worse to hide than to over-report; resolve ambiguity toward surfacing it, never toward quietly averaging it away.
4. Data capture must survive bad venue wifi: save to the device first, sync in the background, never lose a scout's work to a dropped connection.
5. When the AI gets something wrong, fixing it should take one step from wherever the mistake was noticed, not a hunt through raw data to find the source.
