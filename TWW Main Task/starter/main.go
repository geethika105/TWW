package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
)

// store is the single, shared in-memory database for the whole process.
// It is initialised once at startup and is safe for concurrent use because
// Store.Ingest and Store.GetStats both acquire the appropriate mutex lock.
var store = newStore()

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /events", handlePostEvents)
	mux.HandleFunc("GET /campaigns/{campaignID}/stats", handleGetStats)

	addr := ":8080"
	fmt.Printf("listening on %s\n", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

// handlePostEvents ingests a JSON array of events sent by a provider.
//
// Request body:  JSON array — [ { event }, { event }, ... ]
// Response body: JSON object — { "accepted": N, "skipped": N, "rejected": N }
//
// Design decisions:
//
//  1. We decode the body as []json.RawMessage, not []Event. This means a
//     single bad element (e.g. a JSON object with an unparseable field) does
//     NOT fail the entire batch — we process each element individually.
//
//  2. Duplicate events (seen event_id) are silently skipped and counted as
//     "skipped". We still return 200 OK so the provider stops retrying.
//
//  3. Events that fail validation (missing fields, unknown type, bad timestamp)
//     are counted as "rejected". The rest of the batch is still processed.
func handlePostEvents(w http.ResponseWriter, r *http.Request) {
	// Step 1: Decode the outer JSON array into raw elements.
	// Each element is kept as []byte so we can parse and validate them one
	// at a time, without letting one broken event fail the whole request.
	var rawEvents []json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&rawEvents); err != nil {
		// The body itself is not a valid JSON array — reject the whole request.
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "request body must be a JSON array: " + err.Error(),
		})
		return
	}

	accepted, skipped, rejected := 0, 0, 0

	// Step 2: Process each element individually.
	for i, raw := range rawEvents {
		var ev Event
		if err := json.Unmarshal(raw, &ev); err != nil {
			// The element is not a valid JSON object at all.
			log.Printf("event[%d]: JSON parse error: %v", i, err)
			rejected++
			continue
		}

		result, err := store.Ingest(ev)
		switch result {
		case ResultAccepted:
			accepted++
		case ResultDuplicate:
			// Duplicate is not an error — it means the provider retried.
			log.Printf("event[%d] %s: duplicate, skipped", i, ev.EventID)
			skipped++
		case ResultInvalid:
			log.Printf("event[%d] %s: invalid: %v", i, ev.EventID, err)
			rejected++
		}
	}

	// Step 3: Return a summary of what happened to the batch.
	writeJSON(w, http.StatusOK, map[string]int{
		"accepted": accepted,
		"skipped":  skipped,
		"rejected": rejected,
	})
}

// handleGetStats returns aggregated statistics for one campaign.
//
// URL parameter: {campaignID} — e.g. cmp_summer_sale
// Response:      200 with CampaignStats JSON, or 404 if campaign is unknown.
//
// "Unknown campaign" means no valid, non-duplicate event with that campaign_id
// has ever been ingested. We do not require campaigns to be pre-registered.
func handleGetStats(w http.ResponseWriter, r *http.Request) {
	campaignID := r.PathValue("campaignID")

	stats, ok := store.GetStats(campaignID)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": fmt.Sprintf("campaign %q not found", campaignID),
		})
		return
	}

	writeJSON(w, http.StatusOK, stats)
}

// writeJSON sets the Content-Type header, writes the HTTP status code, and
// encodes v as JSON into the response body. Any encoding error is logged but
// cannot be communicated to the caller (headers are already sent).
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("writeJSON: %v", err)
	}
}
