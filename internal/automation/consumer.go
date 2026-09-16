package automation

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/shridarpatil/whatomate/internal/crmevents"
)

// Consumer group settings.
const (
	// ConsumerGroup is shared by every replica, so each event is handled once
	// however many servers are running.
	ConsumerGroup = "automation"

	// ReadBatch bounds how many entries one read claims.
	ReadBatch = 50

	// ReadBlock is how long a read waits for new entries before looping, which
	// is also how quickly the consumer notices it has been asked to stop.
	ReadBlock = 5 * time.Second

	// ReclaimAfter is how long an entry may sit unacknowledged before another
	// consumer takes it over — the replica that had it has probably died.
	ReclaimAfter = time.Minute

	// MaxDeliveries stops an entry that kills its consumer from being retried
	// for ever. Three attempts is enough for a transient failure and few
	// enough to notice a poison message.
	MaxDeliveries = 3
)

// Consumer reads the CRM event stream and runs rules against it.
type Consumer struct {
	Redis  *redis.Client
	Engine *Engine
	Log    Logger

	// Name identifies this replica inside the group. Two replicas sharing a
	// name would steal each other's pending entries.
	Name string
}

func (c *Consumer) log() Logger {
	if c.Log != nil {
		return c.Log
	}
	return slogLogger{slog.Default()}
}

// Run consumes until the context is cancelled.
func (c *Consumer) Run(ctx context.Context) error {
	if c.Redis == nil || c.Engine == nil {
		return errors.New("automation: the consumer needs Redis and an engine")
	}
	if c.Name == "" {
		c.Name = uuid.NewString()
	}

	// MKSTREAM so the group can be created before the first event exists.
	// "$" would skip everything already queued, so "0" is used: an event
	// written while the server was restarting still deserves its rules.
	if err := c.Redis.XGroupCreateMkStream(ctx, crmevents.EventStreamKey, ConsumerGroup, "0").Err(); err != nil &&
		!strings.Contains(err.Error(), "BUSYGROUP") {
		return err
	}

	c.log().Info("Automation consumer started", "consumer", c.Name)

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		// Entries another replica claimed and never acknowledged come first:
		// they are the oldest work and the most likely to be forgotten.
		if err := c.reclaim(ctx); err != nil && !errors.Is(err, redis.Nil) {
			c.log().Error("Automation reclaim failed", "error", err)
		}

		streams, err := c.Redis.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    ConsumerGroup,
			Consumer: c.Name,
			Streams:  []string{crmevents.EventStreamKey, ">"},
			Count:    ReadBatch,
			Block:    ReadBlock,
		}).Result()
		if err != nil {
			if errors.Is(err, redis.Nil) || ctx.Err() != nil {
				continue
			}
			c.log().Error("Automation stream read failed", "error", err)
			time.Sleep(time.Second)
			continue
		}

		for _, stream := range streams {
			for _, entry := range stream.Messages {
				c.handleEntry(ctx, entry)
			}
		}
	}
}

// reclaim takes over entries a dead replica left pending.
func (c *Consumer) reclaim(ctx context.Context) error {
	entries, _, err := c.Redis.XAutoClaim(ctx, &redis.XAutoClaimArgs{
		Stream:   crmevents.EventStreamKey,
		Group:    ConsumerGroup,
		Consumer: c.Name,
		MinIdle:  ReclaimAfter,
		Start:    "0",
		Count:    ReadBatch,
	}).Result()
	if err != nil {
		return err
	}
	for _, entry := range entries {
		c.handleEntry(ctx, entry)
	}
	return nil
}

// handleEntry runs one event's rules and acknowledges it.
func (c *Consumer) handleEntry(ctx context.Context, entry redis.XMessage) {
	payload, _ := entry.Values["payload"].(string)

	event, err := crmevents.DecodeFanout([]byte(payload))
	if err != nil {
		// An entry nothing can parse will never parse. Acknowledging it stops
		// it being redelivered for ever.
		c.log().Error("Automation could not decode an event", "error", err, "entry", entry.ID)
		c.ack(ctx, entry.ID)
		return
	}

	if _, err := c.Engine.Handle(ctx, event); err != nil {
		if c.deliveries(ctx, entry.ID) >= MaxDeliveries {
			c.log().Error("Automation giving up on an event", "error", err,
				"entry", entry.ID, "event_type", event.Type)
			c.ack(ctx, entry.ID)
			return
		}
		// Leave it pending so the next reclaim retries it.
		c.log().Error("Automation failed to handle an event", "error", err, "entry", entry.ID)
		return
	}

	c.ack(ctx, entry.ID)
}

// deliveries reports how many times this entry has been handed out.
func (c *Consumer) deliveries(ctx context.Context, entryID string) int64 {
	pending, err := c.Redis.XPendingExt(ctx, &redis.XPendingExtArgs{
		Stream: crmevents.EventStreamKey,
		Group:  ConsumerGroup,
		Start:  entryID,
		End:    entryID,
		Count:  1,
	}).Result()
	if err != nil || len(pending) == 0 {
		return 0
	}
	return pending[0].RetryCount
}

func (c *Consumer) ack(ctx context.Context, entryID string) {
	if err := c.Redis.XAck(ctx, crmevents.EventStreamKey, ConsumerGroup, entryID).Err(); err != nil {
		c.log().Error("Automation ack failed", "error", err, "entry", entryID)
	}
}
