# Campaign Events

A backend service that receives webhook events from delivery providers and
serves per-campaign statistics to a marketing dashboard.

## Prerequisites

- **Go 1.22 or later** — required for the method+path routing syntax.
  The machine used for this assignment runs **Go 1.27.3**.
  Check your version: `go version`
- No third-party dependencies. Standard library only.

---

## Part 2 — Service

### Start the server

```bash
cd starter/
go run .
# listening on :8080
```

All data is held in memory. It resets when the process restarts — see
*Assumptions* in `NOTES.md` for why that is acceptable here.

### POST /events

Accepts a JSON array of events. Each element is validated and deduplicated
individually; one bad element does not fail the rest of the batch.

```bash
# Single event
curl -s -X POST localhost:8080/events \
  -H 'Content-Type: application/json' \
  -d '[{"event_id":"evt_1","campaign_id":"cmp_summer_sale","contact_id":"ct_001","type":"delivered","timestamp":"2026-08-10T06:30:00Z"}]'
```

```json
{"accepted":1,"rejected":0,"skipped":0}
```

```bash
# Load the seed file (235 events with duplicates, bad timestamps, missing fields)
curl -s -X POST localhost:8080/events \
  -H 'Content-Type: application/json' \
  --data-binary @seed/events.json
```

Response on a fresh server (actual result from this machine):

```json
{"accepted":218,"rejected":7,"skipped":10}
```

Sending the seed file a **second time** to the same running server:

```json
{"accepted":0,"rejected":7,"skipped":228}
```

All previously-seen `event_id` values are skipped. Counters do not change.
This is correct deduplication behaviour — not a bug.

**Response fields**

| Field | Meaning |
|---|---|
| `accepted` | New, valid events stored and counted |
| `skipped` | Duplicate `event_id` — silently ignored, counters unchanged |
| `rejected` | Failed validation: empty field, unknown type, or bad timestamp |

**Validation rules** — an event is rejected if any of these hold:
- `event_id`, `campaign_id`, or `contact_id` is empty or absent
- `type` is not exactly one of `sent`, `delivered`, `opened`, `clicked` (case-sensitive; `OPENED` is rejected)
- `timestamp` is not a valid RFC3339 string (e.g. `"not-a-time"` or `"10-08-2026 09:00"`)

**Duplicate handling** — a duplicate returns `200 OK` (so the retrying
provider stops retrying) and is counted as `skipped`.

### GET /campaigns/{campaign_id}/stats

Returns aggregated counters for one campaign.

```bash
curl -s localhost:8080/campaigns/cmp_summer_sale/stats
```

```json
{
  "campaign_id":  "cmp_summer_sale",
  "sent":         40,
  "delivered":    36,
  "opened":       27,
  "clicked":      12,
  "unique_opens": 19
}
```

`unique_opens` is the number of **distinct contacts** that opened at least
once — not a total open count. A contact opening three times counts as one
unique open.

```bash
# Unknown campaign → 404
curl -s -w "\nHTTP %{http_code}" localhost:8080/campaigns/nonexistent/stats
```

```json
{"error":"campaign \"nonexistent\" not found"}
HTTP 404
```

---

## Part 2 — Tests

```bash
cd starter/

# Run all tests
go test ./...

# Run with the race detector
go test -race ./...

# Verbose output
go test -v -race ./...
```

**Actual results on this machine (Go 1.27.3, race detector enabled):**

```
=== RUN   TestDuplicateEventNotDoubleCount
--- PASS: TestDuplicateEventNotDoubleCount (0.00s)
=== RUN   TestDuplicateInSameBatch
--- PASS: TestDuplicateInSameBatch (0.00s)
=== RUN   TestBadEventInBatchDoesNotKillOthers
--- PASS: TestBadEventInBatchDoesNotKillOthers (0.00s)
=== RUN   TestEmptyEventIDRejected
--- PASS: TestEmptyEventIDRejected (0.00s)
=== RUN   TestUnknownTypeRejected
--- PASS: TestUnknownTypeRejected (0.00s)
=== RUN   TestWrongCaseTypeRejected
--- PASS: TestWrongCaseTypeRejected (0.00s)
=== RUN   TestNonArrayBodyRejected
--- PASS: TestNonArrayBodyRejected (0.00s)
=== RUN   TestStatsUnknownCampaignReturns404
--- PASS: TestStatsUnknownCampaignReturns404 (0.00s)
=== RUN   TestUniqueOpensCountedPerCampaign
--- PASS: TestUniqueOpensCountedPerCampaign (0.00s)
=== RUN   TestConcurrentPostEventsSafe
--- PASS: TestConcurrentPostEventsSafe (0.01s)
PASS
ok  	campaign-events	1.054s
```

10/10 tests pass. No data races detected.

**What each test covers**

| Test | Behaviour verified |
|---|---|
| `TestDuplicateEventNotDoubleCount` | Same `event_id` submitted twice → counter increments only once |
| `TestDuplicateInSameBatch` | Two identical `event_id` values in one array → only first is counted |
| `TestBadEventInBatchDoesNotKillOthers` | Invalid event in the middle of a batch → surrounding valid events still accepted |
| `TestEmptyEventIDRejected` | `"event_id": ""` → rejected |
| `TestUnknownTypeRejected` | `"type": "spam_report"` → rejected |
| `TestWrongCaseTypeRejected` | `"type": "OPENED"` → rejected (case-sensitive) |
| `TestNonArrayBodyRejected` | Body is a JSON object, not an array → `400 Bad Request` |
| `TestStatsUnknownCampaignReturns404` | Campaign with no events → `404 Not Found` |
| `TestUniqueOpensCountedPerCampaign` | Same contact opening in two campaigns → one unique open credited to each |
| `TestConcurrentPostEventsSafe` | 50 goroutines posting simultaneously → final count is exact, no race |

---

## Part 3 — Debugging

```bash
cd debugging/

# Run and compare with expected output
go run . events.jsonl > actual.txt
diff actual.txt expected_output.txt
# No output = output matches exactly

# Run with the race detector
go run -race . events.jsonl
# No "DATA RACE" output = clean
```

**Actual results on this machine:**

```
$ go run . events.jsonl > actual.txt && diff actual.txt expected_output.txt
(no output — exact match)

$ go run -race . events.jsonl 2>race_stderr.txt && wc -c race_stderr.txt
0 race_stderr.txt
(stderr is empty — no data races)
```

See `BUGS.md` for the four bugs that were found and fixed.

---

## Project Structure

```
TWW Main Task/
├── ASSIGNMENT.md          Assignment brief
├── NOTES.md               Parts 1, 4, 5
├── BUGS.md                Part 3 — four bugs documented and fixed
├── AI_USAGE.md            AI assistance disclosure
├── README.md              This file
├── starter/
│   ├── go.mod             Module: campaign-events, Go 1.22
│   ├── main.go            HTTP server + handler implementations
│   ├── store.go           In-memory store, dedup, concurrent safety
│   ├── types.go           Event struct
│   ├── main_test.go       10 tests covering dedup, validation, concurrency
│   └── seed/
│       └── events.json    235 events with duplicates and bad data
└── debugging/
    ├── go.mod
    ├── main.go            Fixed (4 bugs corrected)
    ├── events.jsonl       20,000-line input
    └── expected_output.txt
```
