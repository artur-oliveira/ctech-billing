package realtime

import (
	"context"
	"log/slog"

	"google.golang.org/protobuf/proto"

	"gopkg.aoctech.app/billing/api/internal/realtime/wire"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// Broadcaster is the fan-out transport (ctech-go-common/ws.Registry): one call
// reaches the connections on every instance that registered under the key.
type Broadcaster interface {
	Broadcast(ctx context.Context, key string, payload []byte)
}

// Publisher sends a committed change to the connections allowed to hear it. It
// implements repositories.ChangePublisher and is called after the commit: it
// never returns an error and never fails a mutation, because a change it loses
// is still in the log and a reconnecting client replays it.
type Publisher struct {
	out     Broadcaster
	metrics *Metrics
}

var _ repositories.ChangePublisher = (*Publisher)(nil)

func NewPublisher(out Broadcaster, m *Metrics) *Publisher { return &Publisher{out: out, metrics: m} }

// FrameOf encodes a change as the ServerFrame every connection receives.
func FrameOf(c *repositories.Change) ([]byte, error) {
	return proto.MarshalOptions{Deterministic: true}.Marshal(&wire.ServerFrame{
		Body: &wire.ServerFrame_Change{Change: &wire.Change{
			Id: c.ID, Type: c.Type, Entity: c.Entity, EntityId: c.EntityID, Status: c.Status,
			Revision: c.Revision, OccurredAtMs: uint64(c.OccurredAt.UnixMilli()),
		}},
	})
}

func (p *Publisher) Publish(ctx context.Context, changes ...*repositories.Change) {
	for _, c := range changes {
		frame, err := FrameOf(c)
		if err != nil {
			p.metrics.publishError()
			slog.WarnContext(ctx, "realtime: change not encoded", "change", c.ID, "error", err)
			continue
		}
		for _, scope := range c.Scopes {
			p.out.Broadcast(ctx, scope, frame)
		}
	}
}
