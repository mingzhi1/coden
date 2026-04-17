package protocol

// EventSubscribeParams is the wire format for event.subscribe.
type EventSubscribeParams struct {
	SessionID string `json:"session_id"`
	// SinceSeq requests replay of all ring-buffered events with Seq > SinceSeq
	// before live delivery begins. Zero means live-only (no replay).
	// Set to SessionSnapshotResult.LastEventSeq to close the gap between a
	// snapshot and the start of live event delivery with zero missed events.
	SinceSeq uint64 `json:"since_seq,omitempty"`
}

// EventSubscribeResult acknowledges a session event subscription.
type EventSubscribeResult struct {
	Status    string `json:"status"`
	SessionID string `json:"session_id"`
}

// EventUnsubscribeParams is the wire format for event.unsubscribe.
type EventUnsubscribeParams struct {
	SessionID string `json:"session_id"`
}

// EventUnsubscribeResult acknowledges a session event unsubscription.
type EventUnsubscribeResult struct {
	Status    string `json:"status"`
	SessionID string `json:"session_id"`
}
