# BUGS.md — Debugging Exercise

All four bugs are in [`debugging/main.go`](debugging/main.go).
The program compiled and ran before the fixes but produced wrong output.
All fixes are minimal — no functions were added or removed, no logic was
restructured.

---

## Verification Summary

```
$ go run . events.jsonl > actual.txt
$ diff actual.txt expected_output.txt
(no output — diff is empty)

$ go run -race . events.jsonl 2>race_stderr.txt
$ cat race_stderr.txt
(no output — no data races detected)
```

Both commands run with **Go 1.27.3** on the machine where this assignment
was developed. Output matched `expected_output.txt` exactly on two
consecutive runs.

---

## Bug 1 — `seen` map is local to each batch; cross-batch duplicates are counted twice

### What the bug is

Inside `processBatch()`, the map used to track which `event_id` values have
already been seen was declared as a **local variable**:

```go
// BUGGY — original code
func processBatch(events []Event) {
    seen := make(map[string]bool) // created fresh for every batch
    ...
```

A new, empty `seen` map is created on every call to `processBatch()`.
The file is read in batches of 200 lines (100 batches total for 20,000 lines).
Any duplicate `event_id` that spans two different batches is counted twice.
Requirement 1 says each `event_id` must be counted **at most once across the
whole file**.

### Why it happens

The comment on the local variable says "event IDs we have already processed"
— but "already processed" only meant within the current batch, not across the
whole run. The state was thrown away at the end of each `processBatch()` call.

### The fix (2 lines changed)

Move `seen` to a **package-level variable**, alongside `stats` and `openedBy`:

```go
// FIXED — package-level variable persists across all batch calls
var seen = map[string]bool{}
```

Remove the local `seen := make(map[string]bool)` declaration from
`processBatch()`. The rest of the function (`if seen[ev.EventID]`,
`seen[ev.EventID] = true`) is unchanged.

### Verification

After the fix, the sent/delivered/opened/clicked counters match
`expected_output.txt`. Before the fix, any event_id appearing in two
different batches (i.e. more than 200 lines apart in the file) inflated the
count by one extra.

---

## Bug 2 — `openedBy` key is `contact_id` alone; unique_opens is global, not per-campaign

### What the bug is

The `openedBy` map tracked unique openers using only `contact_id` as the key:

```go
// BUGGY — original code
case "opened":
    if !openedBy[ev.ContactID] {
        openedBy[ev.ContactID] = true
        cs.UniqueOpens++
    }
```

If contact `ct_001` opens a message in `cmp_A`, `openedBy["ct_001"]` is set
to `true`. If the same contact then opens a message in `cmp_B`, the check
`!openedBy["ct_001"]` is `false`, so `cmp_B` never gets its `UniqueOpens`
incremented — even though this is the first open in `cmp_B`.

Requirement 2 states: "The same contact opening in two campaigns counts once
**in each**."

### Why it happens

The key did not include the campaign. Opens from different campaigns shared
the same namespace in the map, so only the first campaign a contact opened in
ever got a unique-open count from that contact.

### The fix (2 lines changed)

Use the compound key `"campaignID/contactID"` to scope opens per campaign:

```go
// FIXED
case "opened":
    key := ev.CampaignID + "/" + ev.ContactID
    if !openedBy[key] {
        openedBy[key] = true
        cs.UniqueOpens++
    }
```

### Verification

The expected output shows `unique_opens` values that require per-campaign
tracking (e.g. `cmp_A: 942`, `cmp_B: 833`, etc.). After the fix, the output
matches these values exactly.

---

## Bug 3 — Daily delivered buckets use local machine time instead of UTC

### What the bug is

In `track()`, the date for the daily delivered bucket was computed using
`.Local()`:

```go
// BUGGY — original code
case "delivered":
    day := ev.Timestamp.Local().Format("2006-01-02")
    cs.DailyDelivered[day]++
```

Requirement 3 says: **"Daily delivered buckets use the event's UTC date."**
The `timestamp` field in the input is always UTC. Using `.Local()` converts to
the machine's configured timezone, which can shift the date — events near
midnight UTC land in the wrong bucket (the day before or after), and the
program produces different results on machines in different timezones.

### Why it happens

`.Local()` is easy to write by habit, but it respects the OS timezone setting.
On a machine in UTC+5:30 (IST), an event at `2026-08-07T22:00:00Z` (which is
`2026-08-07` in UTC) becomes `2026-08-08T03:30:00+05:30` — a different date.

### The fix (1 character changed)

Replace `.Local()` with `.UTC()`:

```go
// FIXED
case "delivered":
    day := ev.Timestamp.UTC().Format("2006-01-02")
    cs.DailyDelivered[day]++
```

### Verification

The expected output shows exactly seven days of buckets (`2026-08-01` through
`2026-08-07`) for each campaign, with specific counts per day. After the fix
the daily bucket counts match exactly. Before the fix, events near midnight
UTC could appear under the wrong date depending on the machine's timezone.

---

## Bug 4 — Race condition: `apply()` increments shared counters without a lock

### What the bug is

`apply()` is called concurrently from 8 worker goroutines. It reads a pointer
to a `CampaignStats` and increments its integer fields:

```go
// BUGGY — original code
func apply(ev Event) {
    cs := stats[ev.CampaignID]
    switch ev.Type {
    case "sent":
        cs.Sent++    // not safe — concurrent write
    ...
```

`cs.Sent++` is not atomic. It compiles to: read the value, add 1, write back.
Two goroutines reading the same value simultaneously, both adding 1, and both
writing back the same result effectively lose one increment.

Requirement 4 says the output must be **identical across runs on the same
input**. A data race makes the output non-deterministic.

### Why it happens

The comment above the `apply()` call site says "The counters are plain ints,
so the increments themselves are cheap." This is true for performance, but
cheap does not mean safe. Plain int increments in Go are **not atomic**. The
race was invisible in casual testing because most runs happened to produce
plausible-looking (but slightly wrong) output.

`go run -race . events.jsonl` on the **original** (unfixed) code reports:

```
DATA RACE
Write at ... by goroutine ...:
  main.apply()
    .../main.go:155 +0x...
Read at ... by goroutine ...:
  main.apply()
    .../main.go:155 +0x...
```

### The fix (3 lines added)

Add a `sync.Mutex` field to `CampaignStats` and lock it at the start of
`apply()`. Each campaign has its own lock, so goroutines processing different
campaigns do not block each other.

```go
// FIXED — mutex field added to struct
type CampaignStats struct {
    mu             sync.Mutex // added
    Sent           int
    ...
}

// FIXED — lock added to apply()
func apply(ev Event) {
    cs := stats[ev.CampaignID]
    cs.mu.Lock()        // added
    defer cs.mu.Unlock() // added
    switch ev.Type {
    ...
```

### Verification

```
$ go run -race . events.jsonl 2>race_stderr.txt
$ cat race_stderr.txt
(empty — no data races)
$ diff <(go run . events.jsonl) expected_output.txt
(empty — output matches exactly)
```

Run twice; both runs produced identical output that matched
`expected_output.txt` byte-for-byte. Exit code was 0 both times.
No `DATA RACE` warnings appeared in stderr with `-race`.
