package services

import (
	"gopkg.aoctech.app/billing/api/internal/domain/byokconn"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// ByokStatement syncs a connection's bank statement into Finanças (Task 15).
type ByokStatement struct{}

// StartBackfill runs the first backfill window in the background.
func (s *ByokStatement) StartBackfill(sp space.ResolvedSpace, conn *byokconn.Connection) {}
