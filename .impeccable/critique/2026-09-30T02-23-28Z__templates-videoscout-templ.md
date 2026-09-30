---
target: Video Scouting feature (templates/videoscout.templ)
total_score: 27
max_score: 40
na_heuristics: 
p0_count: 0
p1_count: 3
target_identity: "file:C:\\Users\\ellio\\OneDrive\\Desktop\\VibeScout\\templates\\videoscout.templ"
target_fingerprint: "sha256:b8c3984f3a9784bb4dd45fa9a748b5885992ff411620f9fd0e4eb6b1297e542c"
target_path: "C:\\Users\\ellio\\OneDrive\\Desktop\\VibeScout\\templates\\videoscout.templ"
timestamp: 2026-09-30T02-23-28Z
slug: templates-videoscout-templ
---
Method: dual-agent (Assessment A: design review · Assessment B: detector + browser evidence), both isolated background subagents.

Method note: both agents' assigned worktrees were pinned to a commit predating the video-scouting feature. Both independently worked around it — one reading source directly from the live project path, the other via `git show post-chezy-changes:...` — and both confirmed real, current content against the live dev server on :8080. Findings below reflect the current, committed feature, not a stale checkout.

## Design Health Score

| # | Heuristic | Score | Key Issue |
|---|---|---|---|
| 1 | Visibility of System Status | 3/4 | Sync badge + live match-card update + pill "Added ✓" are strong multi-channel feedback; the "Select text first" error is comparatively hard to notice (see P1 below). |
| 2 | Match Between System and Real World | 4/4 | Real FRC vocabulary throughout (Q7/Q8, alliance scores, partner/opponent framing). |
| 3 | User Control and Freedom | 2/4 | Clearing a tagged note is one unconfirmed click, and it's a hard DB delete with no undo. |
| 4 | Consistency and Standards | 3/4 | Fits the app's component system cleanly; the "already tagged" gold pill state is inconsistent with itself (see P1). |
| 5 | Error Prevention | 2/4 | No guard against an accidental note wipe; no warning that untagged narrative text never reaches any match. |
| 6 | Recognition Rather Than Recall | 2/4 | Pills remove the need to recall a match number, but the shared gold highlight misleads recognition of what's actually tagged (see P1). |
| 7 | Flexibility and Efficiency of Use | 3/4 | The narrate-then-tag model is a genuine efficiency win over per-match boxes; no way to review "what's tagged so far" without scrolling every card. |
| 8 | Aesthetic and Minimalist Design | 4/4 | Restrained, consistent with the rest of the app's design language. |
| 9 | Help Recognize/Diagnose/Recover from Errors | 2/4 | Exactly one error state exists in the flow, and it's low-salience. |
| 10 | Help and Documentation | 2/4 | The one interaction that needs explaining gets one paragraph of hint text, easy to scroll past. |
| **Total** | | **27/40** | **Acceptable** (right at the top of the band — closer to Good than to Poor) |

## Design Specificity Verdict

**Design review**: Not a reskinned admin form. The four review categories mirror how a human actually watches match film, the match rows use real FRC data (Q-labels, alliance scores, partner/opponent framing, a watch link), and the core interaction — narrate continuously, tag phrases to matches after the fact — reflects a real insight about how a scout actually works rather than a generic "notes" field. Where it slips toward generic-tool territory is layout density: everything (full match list, all four open textareas, every pill row) is live and visible at once with no staging, which reads more like a form-builder default than something shaped around glancing between a phone and a video.

**Deterministic scan**: `impeccable detect` found **zero** findings on both `templates/videoscout.templ` and `templates/layout.templ` via static analysis (confirmed the tool itself works by feeding it a synthetic file with known anti-patterns first). But the **live, rendered** page told a different story: browser-injected detection found **9 anti-patterns** on the review page — repeated `low-contrast` and `gpt-thin-border-wide-shadow` findings, one `line-length` (~117 chars/line vs. the app's own <80 convention), and one `nested-cards`. The team picker and home page each showed the same 3-finding profile (`gpt-thin-border-wide-shadow`, `low-contrast`, `cream-palette` advisory), which — since those two pages share only the global chrome, not video-scouting markup — means that baseline is coming from shared `layout.templ` chrome, not this feature specifically. The review page's *extra* findings above that baseline are what's actually attributable to video scouting.

This static-vs-rendered gap is itself worth noting: `low-contrast` is a computed-color finding invisible to a source-level regex pass, since the app's colors are CSS variables — real signal the CLI pass structurally cannot catch, only the live render can.

**Convergence**: both assessments independently flagged the same contrast problem through different methods — the design review computed `--ps-muted` at 3.99:1 on the match-card panel background, and the browser detector measured the live-rendered `#746b86` on `#f5f1e5` at 4.4:1. Both fail WCAG AA's 4.5:1 minimum. Two independent methods landing on the same real accessibility defect is stronger evidence than either alone, so it's promoted to a Priority Issue below rather than left as a minor note.

## Overall Impression

The interaction model is the strongest thing here — select a phrase, tap a pill, done — and it's engineered correctly, not just conceptually clever (verified live: the mousedown-preventDefault trick that keeps the textarea's selection alive through the button click actually works). The biggest opportunity is that the page tells the truth about *whether* something got tagged but not *what* got tagged where: the shared gold "done" state across all four categories, and the flat, ever-growing pill row, both cost real accuracy at exactly the moment (many matches, time pressure) this feature exists to help with.

## What's Working

1. **The select-then-tag mechanic is functionally solid.** Verified live: the selection survives the button click via `mousedown.preventDefault()`, which is the standard fix for a real, easy-to-get-wrong bug (a plain click would otherwise collapse the selection before the handler ever reads it).
2. **The offline-sync badge is purpose-built for the stated environment**, not a generic toast — three explicit states (offline/syncing/synced), auto-retry on reconnect, visible until actually confirmed. This is infrastructure a generic tool wouldn't bother building.
3. **The live match-card update on tagging closes the loop.** The gold note block appears above immediately, with its own clear button — the single most important thing for discoverability of an otherwise non-obvious interaction, and it works.

## Priority Issues

**[P1] The "already tagged" pill highlight is match-level, not category-level, and actively misleads.**
Why it matters: `HasNote` is one flag per match, reused across all four category pill rows. Tag a Shooting note on Q7 and its Driving/Auto/Failures pills all light up gold too — verified live. A scout scanning the Driving row sees Q7 already gold and reasonably skips it, silently losing data they never actually entered.
Fix: track which category(ies) contributed to a match's note and show that per pill (e.g. small S/D/A/F letter badges), instead of one shared flag.
Suggested command: `/impeccable clarify`

**[P1] Clearing a match note is instant, unconfirmed, and a hard database delete.**
Why it matters: one click on a small ✕ triggers `saveVideoScoutMatchNote` with an empty note, which issues a real `DELETE` — no undo, no confirmation. This is a touch target next to text a thumb naturally rests near while reading, during a live event, with no recovery path if misclicked.
Fix: give the clear action the same reassurance level the tagging path already gets — e.g. a brief "Note removed — Undo" state in the sync badge for a few seconds before the delete actually fires.
Suggested command: `/impeccable harden`

**[P1] "Select text first" feedback is too low-salience to function as real error recovery.**
Why it matters: verified live — the message uses the same neutral color as a normal pill, shifts layout as the button widens, and reverts on a ~1.1s timer. In a time-pressured, glance-at-the-phone context, a scout who looks back at the video for even a second will completely miss it and be left unsure whether anything happened.
Fix: use a distinct warning color (the app already has `.ps-chip-red`), and don't auto-revert on a timer — clear it on the user's next action instead.
Suggested command: `/impeccable clarify`

**[P2] Muted text fails WCAG AA contrast — confirmed two independent ways.**
Why it matters: `--ps-muted` (~`#746b86`) on the cream background/panel measures 3.99–4.4:1 depending on exact background, both below the 4.5:1 minimum for normal text. This affects the "with X, Y / vs A, B" line on every match card.
Fix: darken `--ps-muted` slightly, or reserve it for larger/bold text where the AA threshold is lower (3:1).
Suggested command: `/impeccable audit`

**[P2] Tag-pill rows don't scale to a typical FRC qual schedule.**
Why it matters: verified live by injecting up to 21 synthetic matches — the row wraps without breaking visually, but produces a dense field of 17+ near-identical pills a scout must visually search every time. A real team's 8–12 quals already exceeds the ≤4-items working-memory guideline; this isn't an edge case, it's the common case. 6 of 8 cognitive-load checklist items fail specifically because of this (no chunking, no progressive disclosure, no "one thing at a time").
Fix: group pills (e.g. by recency), or only show a handful plus a "more" disclosure, rather than always listing every match flat.
Suggested command: `/impeccable layout`

## Persona Red Flags

**Casey (Mobile)**: At 375px the layout stacks cleanly, but the ✕ clear button is a bare glyph with `leading-none` and no real padding — a small, high-consequence (irreversible-delete) target sitting right next to the text a thumb rests near while reading it.

**Jordan (First-Timer)**: The entire tag mechanic is taught by one paragraph of gray hint text at the top of the section, easy to scroll past. A first-timer who just starts typing into a big empty textarea (the obvious affordance) produces a perfectly plausible-looking review that's never attributed to any match — confirmed in the data model: untagged text just sits in the four fields with no match linkage at all, and nothing warns that this happened.

**Riley (Stress-Tester)**: Each match's note is a single shared text field appended to across all four categories (confirmed in code) — concurrent edits or a mis-tag just concatenate into one running string with only a `[Label]` text prefix for separation. No conflict handling: last write wins, full overwrite, if the same match were edited from two devices.

## Minor Observations

- `gpt-thin-border-wide-shadow` (thin border + wide shadow, a known generic-AI pattern) appears on every page including the plain home page and team picker — it's coming from shared `layout.templ` chrome (likely `.ps-card`'s border+shadow combo), not from video scouting specifically. Worth a look at some point, but out of this feature's scope.
- `nested-cards` ("card inside card") was flagged on the review page — likely the `.ps-panel` match rows sitting inside the outer `.ps-card` "Matches" section. This is the same nesting pattern used elsewhere in the app for grouping, so it's probably consistent rather than novel, but worth a visual sanity check.
- Line length (~117 chars/line on the review page, vs. the app's own <80-char convention) — likely the hint paragraph or the open textareas at full card width.
- No validation that a `team_number` in the review URL actually exists in the event roster — a mistyped or bookmarked URL silently opens an empty review page for a team that was never at the event. Low risk, but free to fix.
- The `cream-palette` advisory finding on every page reflects the already-established, deliberately-chosen brand palette from earlier this session, not a new issue.
- `iconVideo` is reused for three unrelated actions on one page (home tile, "Switch team," "Save review"), diluting it as the feature's identifying glyph — the rest of the icon set (e.g. `iconSparkle`, reserved solely for AI actions) is more disciplined about one-icon-one-meaning.

## Questions to Consider

1. What if the tag-pill row showed which category(ies) already have content per match (small letter badges) instead of one shared gold state — would that alone fix most of the recognition problem without changing the interaction model?
2. What if clearing a note pushed the deleted text into the sync badge as a 5-second "Note removed — Undo" state, reusing the exact confirmation mechanism the app already built for saving, instead of a whole new confirm-dialog pattern?
3. What if the pill row only ever showed the handful of matches most likely relevant right now (e.g. inferred from which Watch link was opened last) instead of every match a team has played?
