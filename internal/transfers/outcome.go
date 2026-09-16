// Package transfers names what happened when a conversation was handed to a
// person (plan 10, S5).
//
// Handing a conversation over is not a single thing: it may create a queue
// entry, land straight on an agent, be suppressed because the office is shut,
// or do nothing because somebody is already handling it. The old create path
// returned nothing, so every caller — the chatbot, keyword rules, automation —
// reported success regardless, and a rule whose transfer was silently
// suppressed out of hours looked like it had worked.
package transfers

// Outcome is what a transfer attempt actually did.
type Outcome string

const (
	// Assigned means an agent was picked and now holds the conversation.
	Assigned Outcome = "assigned"

	// Queued means the transfer exists but nobody has picked it up. This is
	// the normal result when every agent is busy or the team uses a pull queue.
	Queued Outcome = "queued"

	// SuppressedOutOfHours means the office is shut. The customer gets the
	// out-of-hours message rather than a transfer nobody will see until
	// morning — which is a decision worth reporting, not a silent no-op.
	SuppressedOutOfHours Outcome = "suppressed_out_of_hours"

	// AlreadyActive means somebody is already handling this contact. Creating
	// a second transfer would put the same customer in the queue twice.
	AlreadyActive Outcome = "already_active"

	// Failed means the transfer could not be written.
	Failed Outcome = "failed"
)

// Handed reports whether the conversation actually reached the queue or an
// agent. Callers that need to tell "it worked" from "it was declined for a
// reason" ask this rather than comparing strings.
func (o Outcome) Handed() bool {
	return o == Assigned || o == Queued
}

// String makes the outcome printable in logs and run records.
func (o Outcome) String() string { return string(o) }
