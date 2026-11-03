# NOTES.md

---

## Part 1 — Read and Think

---

### 1. My Interpretation of the Problem

Relay is a messaging platform that sends emails, SMS, and push notifications
on behalf of clients. The actual delivery is handled by third-party providers
(e.g. Mailgun, Twilio). As things happen to each message — it is sent,
delivered, opened, or clicked — the provider sends a webhook event back to
Relay.

My job is to build the service that:

- Receives these events from providers via `POST /events` (accepts a JSON
  array of events in one request).
- Stores them, counting each unique event exactly once (providers retry,
  so the same event may arrive more than once).
- Answers dashboard queries via `GET /campaigns/{campaign_id}/stats`,
  returning counters like how many messages were sent, delivered, opened,
  and clicked for a given campaign.

The service is the bridge between the raw stream of provider callbacks and
the numbers a marketer sees on their live dashboard.

Two real-world constraints shape the design:

- **Providers retry.** If the provider does not receive a `2xx` response in
  time, it sends the same event again. Sometimes it sends duplicates anyway.
  The counters must not increase when a duplicate arrives.
- **Events arrive late and out of order.** An `opened` event for a message
  can arrive before the `delivered` event for the same message. Events can
  show up hours after they actually happened. The service must accept and
  store them regardless of order.

---

### 2. Assumptions

- **Deduplication key is `event_id`.** Two events are considered the same
  if they share the same `event_id`. The assignment states that `event_id`
  is assigned by the provider and identifies one event, so I will use it as
  the uniqueness key.

- **In-memory storage.** The assignment allows either SQLite or in-memory
  persistence. I will use in-memory maps for simplicity. This means all
  data is lost when the service restarts, which is acceptable for this
  assignment.

- **Valid event types are fixed.** Only `sent`, `delivered`, `opened`, and
  `clicked` are meaningful. Events with any other type will be rejected.

- **All required fields must be present and non-empty.** An event missing
  `event_id`, `campaign_id`, `contact_id`, `type`, or `timestamp` is
  invalid and will be rejected individually, without failing the rest of
  the batch.

- **`timestamp` must be a valid RFC3339/ISO8601 UTC time string.** The seed
  data contains malformed timestamps. I will reject events whose timestamp
  cannot be parsed, and log a reason.

- **`unique_opens` means distinct contacts per campaign.** If the same
  contact opens a message five times in one campaign, that counts as one
  unique open for that campaign.

- **Out-of-order events are accepted as-is.** I will not enforce any
  ordering rules (e.g. "delivered must come before opened"). Each valid,
  non-duplicate event is stored and counted regardless of the order it
  arrives.

- **No authentication.** The assignment explicitly says not to add auth.
  Any caller can post events or read stats.

- **No frontend.** The assignment says a UI earns no extra credit. I will
  only build the HTTP API.

- **Concurrent requests must be safe.** Multiple HTTP requests can arrive
  at the same time. The shared in-memory state must be protected with a
  lock to avoid data corruption.

---

### 3. Ambiguities in the Assignment

- **What exactly makes two events "the same"?**
  The brief says `event_id` identifies one event, which strongly suggests
  it is the deduplication key. But it does not say explicitly "deduplicate
  on `event_id` only." For example, should an event with the same
  `event_id` but a different `campaign_id` be treated as a duplicate or a
  conflict?

- **What should the API return for a duplicate event?**
  The brief does not say whether a duplicate should return `200 OK` (silent
  skip), `409 Conflict`, or something else. A provider retrying after a
  timeout expects a `2xx` so it stops retrying — so silently skipping and
  returning `200` is probably the right behaviour, but this is not stated.

- **What should happen when a batch contains both good and bad events?**
  The brief hints at this ("think about what happens to the other 199
  events") but does not specify. I will accept all valid events in a batch
  and reject only the invalid ones, returning a summary of how many were
  accepted, skipped, and rejected.

- **What does "how many opened" mean precisely?**
  Does it mean the total number of `opened` events received (including
  multiple opens by the same contact), or the number of distinct contacts
  who opened? Both are useful metrics. I will return both: `opened` (total
  events) and `unique_opens` (distinct contacts), since the assignment asks
  me to design the response shape myself.

- **What is the exact response shape for `GET /campaigns/{id}/stats`?**
  The assignment says "you design the response shape." I will return a
  JSON object with `campaign_id`, `sent`, `delivered`, `opened`, `clicked`,
  and `unique_opens`.

- **What should `GET /campaigns/{id}/stats` return for a campaign that
  does not exist?** The assignment does not say. I will return `404 Not
  Found` with an error message.

- **Does `metadata` need to be validated or stored?**
  The event payload includes an optional `metadata` field. The assignment
  does not say to do anything with it. I will accept it (so the event is
  not rejected) but not use it in any calculations.

---

### 4. Questions I Would Ask the Product Manager

1. **Deduplication key:** Is `event_id` always globally unique across all
   campaigns and providers, or could two different campaigns reuse the same
   `event_id`? Should I deduplicate on `event_id` alone, or on
   `(event_id, campaign_id)` together?

2. **Duplicate response behaviour:** When a provider retries and we receive
   a duplicate event, should we return `200 OK` (so the provider stops
   retrying) or a different status? What does the provider expect?

3. **Unique opens definition:** For the dashboard, does "opened" mean total
   opens or unique contacts who opened? Should we show both, or just one?

4. **Invalid events in a batch:** If a batch of 100 events has 3 invalid
   ones, should we accept the 97 good ones and reject the 3, or reject the
   whole batch? What does the provider expect back in the response body?

5. **Campaign existence:** Do campaigns need to be pre-registered, or does
   the first event for a campaign implicitly create it? Right now, a
   campaign "exists" as soon as its first event arrives.

6. **Late events:** If an event arrives 3 days late, do we still count it?
   Is there a cutoff? (The assignment implies we accept late events, but I
   want to confirm.)

7. **Stats freshness:** Does the dashboard need real-time stats, or is a
   small delay acceptable? This matters if we ever move from in-memory to
   a database with pre-aggregated counters.

---

### 5. Implementation Priorities

Given the 3.5–4 hour time box, here is what I will build in order:

1. **`POST /events` with deduplication** — This is the core of the service.
   Getting deduplication right is the most important correctness requirement.
   I will use `[]json.RawMessage` decoding so one bad event does not fail
   the whole batch.

2. **`GET /campaigns/{campaign_id}/stats`** — The second required endpoint.
   Once events are stored, reading the counters is straightforward.

3. **Validation** — Reject events with missing fields, invalid type, or
   unparseable timestamp. Return a per-batch summary of accepted / skipped /
   rejected counts.

4. **Concurrent safety** — Protect the in-memory store with a
   `sync.RWMutex`. This is essential for correctness and mentioned
   explicitly in the assignment.

5. **`NOTES.md` — Parts 4 and 5** (scale memo and angry marketer) — Short
   written sections that the evaluators read carefully. Worth doing even
   if I run out of time on code.

6. **`debugging/` — Fix the 4 bugs and write `BUGS.md`** — A self-contained
   task. I will do this after the service is working.

7. **`GET /campaigns/{campaign_id}/events` (paginated list)** — Optional.
   Only if time allows after everything above is done.

8. **Tests** — I will write focused tests for deduplication and partial-batch
   rejection, since those are the behaviours I would be most embarrassed to
   break.

**What I will intentionally skip if time runs short:**
- Pagination on the optional events endpoint
- Persistent storage (SQLite) — in-memory is sufficient per the assignment
- A frontend / UI
- Deployment setup (explicitly out of scope)

---

## Part 4 — Scale Memo

The current service handles a few thousand events a day across ~50 campaigns
using an in-memory store inside a single Go process. Here is what breaks first
and what I would change if a large client pushed traffic to 100 million
events a day.

---

### What breaks first

**RAM.** The `seen` map holds one string per unique `event_id`. At 100 million
events a day, each event_id is roughly 10–15 bytes. That is 1–1.5 GB just for
dedup tracking, before storing any counters. The process will run out of memory
or become very slow as the map grows.

**Single process throughput.** One Go process can handle many concurrent
requests, but it becomes a bottleneck when the request rate is very high. A
single server cannot scale horizontally without shared state.

**In-memory state does not survive restarts.** At this scale, restarts,
deploys, and crashes are frequent. Losing all data on restart is not acceptable.

---

### What I would change, and in what order

1. **Move counters to a real database (e.g. PostgreSQL or Redis).**
   Pre-aggregated counters (one row per campaign per event type) are fast
   to read and write. The stats query becomes a simple SELECT.

2. **Use Redis for deduplication.** `SETNX event_id` with a TTL gives fast,
   distributed dedup. A Bloom filter is an option if memory matters more than
   exact correctness.

3. **Add a message queue (Kafka or SQS) in front of the service.**
   Providers post to the queue. Workers consume from the queue and write to the
   database. This decouples ingestion speed from processing speed and handles
   traffic spikes gracefully.

4. **Run multiple service instances behind a load balancer.**
   Once state is in a database, any instance can handle any request.

---

### API contract

The API contract (`POST /events`, `GET /campaigns/{id}/stats`) does not need to
change. The response shape stays the same. Internally, reads may become slightly
delayed if we move to async processing, but for most use cases a few seconds of
lag is acceptable — dashboards do not need millisecond freshness.

---

### Where duplicates can sneak in with a queue

A message queue introduces **at-least-once delivery**: if a worker crashes
after processing a message but before acknowledging it, the queue resends the
message. This creates a new source of duplicates that did not exist before.

The deduplication layer (Redis SETNX or a DB unique index on event_id) still
handles this, but the window for duplicates gets wider. We must ensure the
dedup check and the counter write happen atomically (e.g. inside a database
transaction) to avoid race conditions across workers.

---

### Keeping counters trustworthy

- Use database transactions: read current count, add, write — atomically.
- Or use atomic increment operations (Redis INCR, Postgres UPDATE ... RETURNING).
- Add monitoring on the dedup miss rate: a sudden spike means something is
  generating new event_ids for old events, which is worth investigating.

---

### What I would monitor

- Events accepted / skipped / rejected per second.
- Queue depth (how far behind are workers?).
- Dedup hit rate.
- p99 latency for `POST /events` and `GET /campaigns/{id}/stats`.
- Memory usage of the dedup store.

---

### What I would deliberately not solve yet

- Per-contact event history — it is not required by the dashboard.
- Multi-region replication — premature at this stage.
- Archiving old events — useful eventually, but not on day one.
- Authentication — the assignment explicitly excludes it.

---

## Part 5 — The Angry Marketer

At 10:00 the dashboard shows `Delivered: 970,000 / Opened: 250,000`.
At 10:30 it shows `Delivered: 975,000 / Opened: 248,000`.
Delivered went up. Opened went down. The marketer files a ticket.

---

### Plausible explanations before assuming the code is broken

1. **Late-arriving delivered events.** Providers can report delivered hours
   after the fact. Five thousand more delivered events arrived between 10:00
   and 10:30. This is normal and expected.

2. **Duplicate opened events were removed.** Between 10:00 and 10:30, a
   batch of opened events arrived with event_ids that had already been seen.
   The dedup layer correctly skipped them, but the first batch (before dedup
   was applied) may have already been counted. If the service restarted or a
   bug was fixed mid-run, the numbers could drop on the second read.

3. **An upstream provider sent a correction.** Some providers issue correction
   or retraction events when they discover they reported a false positive. If
   the service processed a correction that invalidated some opened events, the
   count drops.

4. **Two different providers reported the same event.** If two providers both
   tried to deliver the same message and both reported "opened," but dedup
   only runs on event_id (which differs per provider), the first count inflated
   the number. If one was later identified as a duplicate at the provider level,
   a cleanup removed it.

5. **Timezone or clock skew.** Events timestamped near midnight can shift
   between days depending on how the server processes them. If the dashboard
   filters by time window and the window boundaries shifted, events could move
   in or out of the visible range.

6. **In-memory store restarted.** If the service process was restarted between
   10:00 and 10:30, events re-ingested from a replay would re-run dedup and
   could land differently if some data was already in the queue.

---

### What I would check first

1. **Service logs between 10:00 and 10:30.** Look for restarts, large rejected
   batches, or anything unusual.
2. **Raw event counts in storage.** Count accepted opened events in the
   database directly, bypassing the API. If the raw count is also 248,000,
   the API is reporting correctly.
3. **Dedup log.** How many events were skipped as duplicates in that window?
   A surge in skipped events could explain the drop.
4. **Provider traffic.** Did a provider send a large late batch of delivered
   events between 10:00 and 10:30?

---

### Is the delivered number rising suspicious?

No, not by itself. Late-arriving delivered events are explicitly mentioned in
the assignment as a real-world fact. A rise of 5,000 out of 1,000,000 (0.5%)
is within normal range for a provider that batches its callbacks.

The opened drop is the more interesting signal. Combined with the delivered
rise, the most likely explanation is: a large batch of events arrived in that
window, some were delivered (new, accepted), and some were opened (already
seen, skipped by dedup). Both numbers are correct — the dashboard is not broken.

I would tell the marketer: "The drop in opens is most likely duplicates being
correctly filtered. The rise in delivered is normal late reporting. I'll confirm
by checking the logs and will follow up within the hour."

---

## What I Completed / What I Intentionally Skipped

### Completed
- `POST /events` with per-event validation, deduplication, and partial-batch
  acceptance using `[]json.RawMessage`.
- `GET /campaigns/{campaign_id}/stats` returning sent, delivered, opened,
  clicked, and unique_opens.
- In-memory store protected by `sync.RWMutex` for safe concurrent access.
- Tests for duplicate handling, invalid events, concurrent requests, and
  unique_opens correctness.
- All 4 bugs in `debugging/main.go` identified and fixed minimally.
- `BUGS.md` documenting each bug.
- `NOTES.md` Parts 1, 4, and 5.
- `AI_USAGE.md`.
- `README.md` with exact run and test instructions.

### Intentionally Skipped
- `GET /campaigns/{campaign_id}/events` (paginated list) — optional per the
  assignment; deprioritised given the time constraint.
- SQLite persistence — in-memory is explicitly allowed; adding SQLite would
  not change the API or demonstrate anything new within this time box.
- Frontend / UI — explicitly earns no credit.
- Authentication and deployment setup — explicitly out of scope.

---

## If I Had Another Day

- **Persistent storage (SQLite).** Losing data on restart would not be
  acceptable in a real system. I would add a SQLite backend with a unique
  index on event_id for dedup and a stats table for fast reads.
- **Paginated `GET /campaigns/{id}/events`.** The optional endpoint is genuinely
  useful for a "recent activity" view.
- **Structured logging.** Replace `log.Printf` with a JSON logger so logs can
  be queried in production.
- **More test coverage.** Particularly: submitting the full seed file and
  asserting exact counters, and testing the race detector output formally.
- **Graceful shutdown.** Handle `SIGINT`/`SIGTERM` so in-flight requests
  finish before the process exits.
