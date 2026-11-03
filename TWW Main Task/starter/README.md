# campaign-events starter

A minimal skeleton. You can change anything in it — the routing, the types,
the layout. It exists so you don't spend your first twenty minutes on
boilerplate.

## Requirements

- Go 1.22 or later (the `"POST /events"` routing pattern needs it).
  Check with `go version`. On older Go, replace the patterns with manual
  method/path handling — that's fine too.
- No third-party dependencies are required. You may add them
  (e.g. an SQLite driver) if you want; keep `go.mod` tidy.

## Run

    go run .

Then, in another terminal:

    curl -s -X POST localhost:8080/events \
      -H 'Content-Type: application/json' \
      -d '[{"event_id":"evt_1","campaign_id":"cmp_summer_sale","contact_id":"ct_001","type":"delivered","timestamp":"2026-08-10T06:15:00Z"}]'

    curl -s localhost:8080/campaigns/cmp_summer_sale/stats

Both return 501 until you implement them.

## Seed data

`seed/events.json` is one day of realistic provider traffic: 235 lines of events
including retries, conflicts, and malformed records. Your service should
survive all of it. Surviving it is not the same as accepting all of it — what
you accept, reject, and normalise is yours to decide and document.

One practical note: `[]Event` with strict field types means one bad element
can fail the whole array decode. Whether that is acceptable behaviour for a
batch endpoint is one of the decisions we are interested in.

To load the seed:

    curl -s -X POST localhost:8080/events \
      -H 'Content-Type: application/json' \
      --data-binary @seed/events.json
