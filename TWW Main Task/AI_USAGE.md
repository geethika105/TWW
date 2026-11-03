# AI_USAGE.md

## Tools Used

- **Google Antigravity (Claude Sonnet 4.6)** — used throughout the session as
  a pair-programming assistant inside the IDE.

---

## What I Used AI For

- **Explaining Go concepts** — understanding `sync.RWMutex`, the difference
  between `RLock`/`Lock`, and why `defer` is safe to use inside a loop
  iteration.
- **Reviewing the seed data** — asked AI to catalogue all the messy cases in
  `seed/events.json` (duplicates, bad timestamps, missing fields) so I could
  make sure my validation covered all of them.
- **Drafting documentation** — Parts 4 and 5 of `NOTES.md` and the structure
  of `BUGS.md` were drafted with AI assistance and then reviewed and edited
  for accuracy.
- **Identifying the debugging bugs** — AI helped me reason through the race
  condition (Bug 4) and confirm that `sync.Mutex` inside the struct was a
  minimal fix.

---

## One Suggestion I Rejected or Changed

AI initially suggested using `sync/atomic` operations (e.g. `atomic.AddInt64`)
for the counter increments in Bug 4 of the debugging exercise. I rejected this
because it would have required changing the field types from `int` to
`int64` across the whole struct, touching more lines than necessary. Instead,
I added a `sync.Mutex` field to `CampaignStats` — a more surgical change that
keeps the original types intact, which is what "minimal fix" means in this
context.

---

## One Thing AI Helped Me Understand

AI clearly explained why `json.RawMessage` is the right choice for batch
decoding. I initially assumed I should decode `[]Event` and handle errors
per-element, but AI pointed out that the JSON decoder stops at the first
error when decoding into a typed slice — meaning one bad element would fail
the entire batch. Decoding into `[]json.RawMessage` first preserves the raw
bytes of each element so they can be parsed individually.

---

## Anything AI Generated That I Had to Debug

The initial test helper `getStats` in `main_test.go` used a real `ServeMux`
router to set the path value, which would have been more complex than needed.
AI suggested using `req.SetPathValue("campaignID", campaignID)` directly on
the request — a simpler approach available in Go 1.22 — which I adopted after
verifying it in the Go documentation.
