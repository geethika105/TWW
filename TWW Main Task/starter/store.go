package main

import (
	"fmt"
	"sync"
	"time"
)

// validTypes is the set of event types the spec defines.
// Only lowercase, exact matches are accepted.
var validTypes = map[string]bool{
	"sent":      true,
	"delivered": true,
	"opened":    true,
	"clicked":   true,
}

// CampaignStats holds the counters that the dashboard cares about.
// All fields are exported so encoding/json can serialise them.
type CampaignStats struct {
	CampaignID  string `json:"campaign_id"`
	Sent        int    `json:"sent"`
	Delivered   int    `json:"delivered"`
	Opened      int    `json:"opened"`
	Clicked     int    `json:"clicked"`
	UniqueOpens int    `json:"unique_opens"`
}

// Store is the in-memory database for the service.
//
// Design notes:
//   - mu protects all three maps. Reads acquire RLock; writes acquire Lock.
//   - seen maps event_id → true once an event has been processed.
//     Any event_id that already exists in seen is a duplicate and is skipped.
//   - stats maps campaign_id → pointer to its CampaignStats.
//     Using a pointer means we update counters in-place without re-inserting.
//   - openers maps campaign_id → set of contact_ids that have opened.
//     A Go set is represented as map[string]bool (value is always true).
//     This is how we count unique_opens without storing every raw event.
type Store struct {
	mu      sync.RWMutex
	seen    map[string]bool
	stats   map[string]*CampaignStats
	openers map[string]map[string]bool
}

// newStore creates and returns an empty, ready-to-use Store.
func newStore() *Store {
	return &Store{
		seen:    make(map[string]bool),
		stats:   make(map[string]*CampaignStats),
		openers: make(map[string]map[string]bool),
	}
}

// IngestResult describes what happened to a single event.
type IngestResult string

const (
	ResultAccepted  IngestResult = "accepted"
	ResultDuplicate IngestResult = "duplicate"
	ResultInvalid   IngestResult = "invalid"
)

// IngestError pairs an IngestResult with a human-readable reason.
// Used only for invalid events so the caller can log or report why.
type IngestError struct {
	Result IngestResult
	Reason string
}

func (e *IngestError) Error() string { return e.Reason }

// validate checks that ev has all required fields and a known type.
// It also parses and returns the timestamp as a time.Time value.
// Returns an *IngestError if anything is wrong.
func validate(ev Event) (time.Time, error) {
	if ev.EventID == "" {
		return time.Time{}, &IngestError{ResultInvalid, "event_id is empty"}
	}
	if ev.CampaignID == "" {
		return time.Time{}, &IngestError{ResultInvalid, "campaign_id is empty"}
	}
	if ev.ContactID == "" {
		return time.Time{}, &IngestError{ResultInvalid, "contact_id is empty"}
	}
	if !validTypes[ev.Type] {
		return time.Time{}, &IngestError{ResultInvalid, fmt.Sprintf("unknown type %q", ev.Type)}
	}
	t, err := time.Parse(time.RFC3339, ev.Timestamp)
	if err != nil {
		return time.Time{}, &IngestError{ResultInvalid, fmt.Sprintf("invalid timestamp %q", ev.Timestamp)}
	}
	return t, nil
}

// Ingest validates and stores one event.
//
// Returns:
//   - ResultAccepted  — event was new, valid, and stored.
//   - ResultDuplicate — event_id has been seen before; counters unchanged.
//   - ResultInvalid   — validation failed; err explains why.
func (s *Store) Ingest(ev Event) (IngestResult, error) {
	// Validate before acquiring the lock — keeps the critical section short.
	if _, err := validate(ev); err != nil {
		return ResultInvalid, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Deduplication check.
	if s.seen[ev.EventID] {
		return ResultDuplicate, nil
	}
	s.seen[ev.EventID] = true

	// Lazily initialise stats for a campaign the first time we see it.
	if _, ok := s.stats[ev.CampaignID]; !ok {
		s.stats[ev.CampaignID] = &CampaignStats{CampaignID: ev.CampaignID}
		s.openers[ev.CampaignID] = make(map[string]bool)
	}

	cs := s.stats[ev.CampaignID]

	switch ev.Type {
	case "sent":
		cs.Sent++
	case "delivered":
		cs.Delivered++
	case "opened":
		cs.Opened++
		// Track unique opens per campaign.
		// openers[campaign][contact] = true only the first time.
		// len(openers[campaign]) is then the unique-open count.
		if !s.openers[ev.CampaignID][ev.ContactID] {
			s.openers[ev.CampaignID][ev.ContactID] = true
			cs.UniqueOpens++
		}
	case "clicked":
		cs.Clicked++
	}

	return ResultAccepted, nil
}

// GetStats returns a copy of the stats for the given campaign.
// The second return value is false when the campaign has no recorded events.
//
// We return a copy (not a pointer) so the caller can read it after releasing
// the lock — no risk of data races on the caller's side.
func (s *Store) GetStats(campaignID string) (CampaignStats, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	cs, ok := s.stats[campaignID]
	if !ok {
		return CampaignStats{}, false
	}
	// Return a value copy, not the pointer stored in the map.
	return *cs, true
}
