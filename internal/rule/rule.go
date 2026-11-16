// Package rule is the only extension point for a new drinking-table game.
// A plugin implements Rule and registers itself from init. The HTTP surface
// does not grow when a game is added.
package rule

import (
	"encoding/json"
	"time"
)

const (
	ScopePublic = -1
	TurnTimeout = 20 * time.Second
	EventSettle = "Settle"
)

// StartContext is the public input of a new hand. Seats are 1-based and
// already exclude players who left the table.
type StartContext struct {
	PlayID   string
	RoomID   string
	GameCode string
	Variant  string
	Config   json.RawMessage
	Seats    []int
}

// Action is one client SubmitAction. Payload is raw JSON, or empty.
type Action struct {
	Type    string
	Payload json.RawMessage
}

// Event is appended to the play log. Scope -1 is visible to every seat.
// Any other scope is visible only to that seat. Payload must be JSON-friendly.
// When the hand ends, emit exactly one public event Type "Settle" whose
// payload is {"Scores":[{"Seat":1,"Delta":1,"Detail":{}}]}. Deltas must cover
// every seat that started the hand.
type Event struct {
	Type    string
	Scope   int
	Payload any
}

// State is the authoritative blob stored in MySQL. Data belongs to the plugin.
type State struct {
	Phase    string          `json:"Phase"`
	Data     json.RawMessage `json:"Data"`
	Settled  bool            `json:"Settled"`
	Deadline *time.Time      `json:"Deadline,omitempty"`
}

// ActionSpec tells the client which buttons to show. It is not authorization.
// Apply still rejects illegal actions.
type ActionSpec struct {
	Type string `json:"Type"`
	Hint string `json:"Hint,omitempty"`
}

// Snapshot is what one seat is allowed to see.
type Snapshot struct {
	Phase    string
	Public   json.RawMessage
	Private  json.RawMessage
	Settled  bool
	Deadline *time.Time
	Legal    []ActionSpec
}

// Rule is a pure state machine. Implementations must not touch the database
// or the network. Randomness goes through this package's Intn and Shuffle.
type Rule interface {
	Code() string
	MinSeats() int
	MaxSeats() int
	Start(ctx StartContext) (State, []Event, error)
	Apply(state State, seat int, action Action, now time.Time) (State, []Event, error)
	View(state State, seat int) (Snapshot, error)
	// OnTimeout is called only after Deadline has passed. It must move the
	// hand forward: next seat, auto action, or settlement.
	OnTimeout(state State, now time.Time) (State, []Event, error)
}

func After(d time.Duration) *time.Time {
	t := time.Now().UTC().Add(d).Truncate(time.Millisecond)
	return &t
}

func MustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func Decode(raw json.RawMessage, dest any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return json.Unmarshal(raw, dest)
}

func Normalize(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return json.RawMessage(`{}`)
	}
	return raw
}

func MarshalPayload(v any) (json.RawMessage, error) {
	if v == nil {
		return json.RawMessage(`{}`), nil
	}
	if raw, ok := v.(json.RawMessage); ok {
		return Normalize(raw), nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return b, nil
}
