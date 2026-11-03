package main

// Event is one webhook event from a message provider.
//
// Note: this struct is a starting point, not a constraint. If decoding the
// whole request into []Event doesn't give you the error handling you want,
// change the approach (json.RawMessage per item is one option).
type Event struct {
	EventID    string            `json:"event_id"`
	CampaignID string            `json:"campaign_id"`
	ContactID  string            `json:"contact_id"`
	Type       string            `json:"type"` // sent | delivered | opened | clicked
	Timestamp  string            `json:"timestamp"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}
