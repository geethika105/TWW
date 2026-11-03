package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// --- helpers ----------------------------------------------------------------

// freshStore replaces the global store with an empty one for each test.
// Tests share the global store variable, so we reset it to avoid cross-test
// contamination.
func freshStore() {
	store = newStore()
}

// postEvents sends a JSON array to the test server and returns the decoded
// response body as a map.
func postEvents(t *testing.T, payload string) map[string]int {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/events",
		bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handlePostEvents(w, req)

	var result map[string]int
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v\nbody: %s", err, w.Body.String())
	}
	return result
}

// getStats fetches stats for a campaign and returns the decoded CampaignStats.
func getStats(t *testing.T, campaignID string) (CampaignStats, int) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet,
		"/campaigns/"+campaignID+"/stats", nil)
	// PathValue requires a real mux — set it via the pattern trick.
	req.SetPathValue("campaignID", campaignID)
	w := httptest.NewRecorder()
	handleGetStats(w, req)

	if w.Code == http.StatusNotFound {
		return CampaignStats{}, http.StatusNotFound
	}
	var cs CampaignStats
	if err := json.NewDecoder(w.Body).Decode(&cs); err != nil {
		t.Fatalf("failed to decode stats: %v", err)
	}
	return cs, w.Code
}

// oneEvent builds a single-element JSON array from explicit field values.
func oneEvent(eventID, campaignID, contactID, evType, timestamp string) string {
	return `[{"event_id":"` + eventID + `","campaign_id":"` + campaignID +
		`","contact_id":"` + contactID + `","type":"` + evType +
		`","timestamp":"` + timestamp + `"}]`
}

const (
	goodTime   = "2026-08-10T09:00:00Z"
	goodCmp    = "cmp_test"
	goodCtID   = "ct_001"
	goodEvID   = "evt_test_001"
	goodEvID2  = "evt_test_002"
)

// --- duplicate handling tests -----------------------------------------------

// TestDuplicateEventNotDoubleCount verifies that sending the same event_id
// twice does not increment any counter a second time.
func TestDuplicateEventNotDoubleCount(t *testing.T) {
	freshStore()
	payload := oneEvent(goodEvID, goodCmp, goodCtID, "sent", goodTime)

	r1 := postEvents(t, payload)
	r2 := postEvents(t, payload)

	// First submission: accepted.
	if r1["accepted"] != 1 {
		t.Errorf("first submission: want accepted=1, got %d", r1["accepted"])
	}
	// Second submission: skipped, not accepted.
	if r2["accepted"] != 0 || r2["skipped"] != 1 {
		t.Errorf("second submission: want accepted=0,skipped=1, got %+v", r2)
	}
	// Counter must be 1, not 2.
	cs, code := getStats(t, goodCmp)
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if cs.Sent != 1 {
		t.Errorf("sent counter: want 1, got %d", cs.Sent)
	}
}

// TestDuplicateInSameBatch verifies that duplicate event_ids within a single
// batch are also caught.
func TestDuplicateInSameBatch(t *testing.T) {
	freshStore()
	// Two objects with the same event_id in one array.
	payload := `[
		{"event_id":"evt_dup","campaign_id":"cmp_x","contact_id":"ct_1","type":"sent","timestamp":"2026-08-10T09:00:00Z"},
		{"event_id":"evt_dup","campaign_id":"cmp_x","contact_id":"ct_1","type":"sent","timestamp":"2026-08-10T09:00:00Z"}
	]`
	r := postEvents(t, payload)
	if r["accepted"] != 1 || r["skipped"] != 1 {
		t.Errorf("want accepted=1,skipped=1, got %+v", r)
	}
	cs, _ := getStats(t, "cmp_x")
	if cs.Sent != 1 {
		t.Errorf("want sent=1, got %d", cs.Sent)
	}
}

// --- invalid event tests ----------------------------------------------------

// TestBadEventInBatchDoesNotKillOthers verifies that one invalid event in a
// batch does not prevent valid events from being processed.
func TestBadEventInBatchDoesNotKillOthers(t *testing.T) {
	freshStore()
	// Array with one invalid event (bad timestamp) sandwiched between two valid ones.
	payload := `[
		{"event_id":"evt_good_1","campaign_id":"cmp_y","contact_id":"ct_1","type":"sent","timestamp":"2026-08-10T09:00:00Z"},
		{"event_id":"evt_bad","campaign_id":"cmp_y","contact_id":"ct_1","type":"sent","timestamp":"not-a-time"},
		{"event_id":"evt_good_2","campaign_id":"cmp_y","contact_id":"ct_2","type":"delivered","timestamp":"2026-08-10T09:01:00Z"}
	]`
	r := postEvents(t, payload)
	if r["accepted"] != 2 || r["rejected"] != 1 {
		t.Errorf("want accepted=2,rejected=1, got %+v", r)
	}
	cs, _ := getStats(t, "cmp_y")
	if cs.Sent != 1 || cs.Delivered != 1 {
		t.Errorf("want sent=1,delivered=1, got sent=%d,delivered=%d",
			cs.Sent, cs.Delivered)
	}
}

// TestEmptyEventIDRejected verifies that an event with an empty event_id
// is rejected.
func TestEmptyEventIDRejected(t *testing.T) {
	freshStore()
	payload := `[{"event_id":"","campaign_id":"cmp_z","contact_id":"ct_1","type":"sent","timestamp":"2026-08-10T09:00:00Z"}]`
	r := postEvents(t, payload)
	if r["rejected"] != 1 {
		t.Errorf("want rejected=1, got %+v", r)
	}
}

// TestUnknownTypeRejected verifies that an unrecognised event type is rejected.
func TestUnknownTypeRejected(t *testing.T) {
	freshStore()
	payload := `[{"event_id":"evt_x","campaign_id":"cmp_z","contact_id":"ct_1","type":"spam_report","timestamp":"2026-08-10T09:00:00Z"}]`
	r := postEvents(t, payload)
	if r["rejected"] != 1 {
		t.Errorf("want rejected=1, got %+v", r)
	}
}

// TestWrongCaseTypeRejected verifies that "OPENED" (uppercase) is not accepted.
func TestWrongCaseTypeRejected(t *testing.T) {
	freshStore()
	payload := `[{"event_id":"evt_case","campaign_id":"cmp_z","contact_id":"ct_1","type":"OPENED","timestamp":"2026-08-10T09:00:00Z"}]`
	r := postEvents(t, payload)
	if r["rejected"] != 1 {
		t.Errorf("want rejected=1, got %+v", r)
	}
}

// TestNonArrayBodyRejected verifies that a non-array body returns 400.
func TestNonArrayBodyRejected(t *testing.T) {
	freshStore()
	req := httptest.NewRequest(http.MethodPost, "/events",
		bytes.NewBufferString(`{"event_id":"e1"}`)) // object, not array
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handlePostEvents(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}

// --- stats tests ------------------------------------------------------------

// TestStatsUnknownCampaignReturns404 verifies that querying a campaign with
// no events returns 404.
func TestStatsUnknownCampaignReturns404(t *testing.T) {
	freshStore()
	_, code := getStats(t, "does_not_exist")
	if code != http.StatusNotFound {
		t.Errorf("want 404, got %d", code)
	}
}

// TestUniqueOpensCountedPerCampaign verifies that unique_opens tracks distinct
// contacts per campaign, not globally. The same contact opening in two
// campaigns should contribute one unique open to each.
func TestUniqueOpensCountedPerCampaign(t *testing.T) {
	freshStore()
	// ct_001 opens in cmp_A.
	postEvents(t, `[{"event_id":"e1","campaign_id":"cmp_A","contact_id":"ct_001","type":"opened","timestamp":"2026-08-10T09:00:00Z"}]`)
	// ct_001 opens in cmp_B.
	postEvents(t, `[{"event_id":"e2","campaign_id":"cmp_B","contact_id":"ct_001","type":"opened","timestamp":"2026-08-10T09:00:00Z"}]`)
	// ct_001 opens in cmp_A again (different event_id but same contact).
	postEvents(t, `[{"event_id":"e3","campaign_id":"cmp_A","contact_id":"ct_001","type":"opened","timestamp":"2026-08-10T10:00:00Z"}]`)

	a, _ := getStats(t, "cmp_A")
	b, _ := getStats(t, "cmp_B")

	// cmp_A: 2 total opens, 1 unique (same contact opened twice).
	if a.Opened != 2 || a.UniqueOpens != 1 {
		t.Errorf("cmp_A: want opened=2,unique_opens=1, got opened=%d,unique_opens=%d",
			a.Opened, a.UniqueOpens)
	}
	// cmp_B: 1 total open, 1 unique.
	if b.Opened != 1 || b.UniqueOpens != 1 {
		t.Errorf("cmp_B: want opened=1,unique_opens=1, got opened=%d,unique_opens=%d",
			b.Opened, b.UniqueOpens)
	}
}

// --- concurrency test -------------------------------------------------------

// TestConcurrentPostEventsSafe fires 50 goroutines each posting a unique event
// to the same campaign, then verifies the final count is exactly 50.
// Run with: go test -race ./...
func TestConcurrentPostEventsSafe(t *testing.T) {
	freshStore()
	const n = 50
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i // capture loop variable
		go func() {
			defer wg.Done()
			// Use a unique event_id per goroutine.
			evID := "evt_concurrent_" + string(rune('a'+i%26)) + string(rune('0'+i/26))
			payload := `[{"event_id":"` + evID + `","campaign_id":"cmp_concurrent","contact_id":"ct_001","type":"sent","timestamp":"2026-08-10T09:00:00Z"}]`
			req := httptest.NewRequest(http.MethodPost, "/events",
				bytes.NewBufferString(payload))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			handlePostEvents(w, req)
		}()
	}
	wg.Wait()

	cs, code := getStats(t, "cmp_concurrent")
	if code != http.StatusOK {
		t.Fatalf("want 200, got %d", code)
	}
	if cs.Sent != n {
		t.Errorf("concurrent sends: want sent=%d, got %d", n, cs.Sent)
	}
}
