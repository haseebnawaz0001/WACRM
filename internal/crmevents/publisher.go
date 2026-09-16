package crmevents

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ErrUnknownEventType is returned when publishing a type that is not in the
// catalog. Catching it at the publish site keeps typos from becoming events
// that no sink will ever handle.
var ErrUnknownEventType = errors.New("crmevents: unknown event type")

// PublishTx writes the event to the outbox using the caller's transaction.
//
// Pass the same *gorm.DB that is performing the change, so the event and the
// change commit or roll back together. This is the only publish path that
// guarantees the two stay consistent, and it is what every mutation should
// use.
//
// It performs a single INSERT and does not fan out; the relay does that after
// the transaction commits.
func PublishTx(tx *gorm.DB, e Event) error {
	if e.OrgID == uuid.Nil {
		// Reject rather than writing an org-less row the relay could
		// never scope to a webhook subscriber.
		return fmt.Errorf("crmevents: event %q has no organization", e.Type)
	}
	if !IsKnown(e.Type) {
		return fmt.Errorf("%w: %s", ErrUnknownEventType, e.Type)
	}
	return tx.Create(e.toRow()).Error
}

// Publish writes the event to the outbox outside any transaction.
//
// Use it only where there is no surrounding transaction to join — a crash
// between the change and this call loses the event, which is exactly the
// failure mode PublishTx exists to prevent.
func Publish(db *gorm.DB, e Event) error {
	return PublishTx(db, e)
}
