package v1

import (
	"context"
	"log/slog"
	"time"

	"gopkg.aoctech.app/api-commons/health"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

// The two health endpoints come from api-commons/health (draft-inadarei).
//
//   - `/v1.0/health` is liveness: dependency-free, always 200 while the process
//     is up, and what HAProxy probes.
//   - `/v1.0/health-check` is the dependency report (200/207/503), for a person
//     and for monitoring.
//
// **The balancer never probes the report.** There is no target group here
// (terraform/README.md): HAProxy probes directly with `healthyStatuses = [200]`
// and `autoHeal = true`, so a status the report returns is a status that gets an
// instance *replaced*. A DynamoDB outage would recycle the whole fleet at once.
// Only DynamoDB is Primary (503); Valkey, CPU, memory and the clock warn (207).

const (
	healthAPIVersion  = "/v1"
	healthServiceID   = "CTech Billing"
	healthDescription = "Health check details for CTech Billing API"

	componentClock = "clock"
	measureUTCOff  = "utcOffset"
)

// newHealth builds the handler from the report's inputs. All of db, cache and
// table may be zero (a test wires the app without them): the report then says so
// instead of panicking, as DynamoDB fail (nothing works without the tables) and
// the cache warn (a single-instance deployment runs on the in-memory fallback).
func newHealth(d Deps, now func() time.Time) *health.Handler {
	var ping func(context.Context) error
	if d.Cache != nil {
		ping = d.Cache.Ping
	}
	h := health.New(health.Config{
		ServiceID:   healthServiceID,
		Description: healthDescription,
		Version:     healthAPIVersion,
		ReleaseID:   d.AppVersion,
	})
	// A nil *dynamodb.Client must not become a non-nil interface.
	var db health.TableDescriber
	if d.DB != nil {
		db = d.DB
	}
	return h.Add(
		health.DynamoDB(db, d.InvoicesTable, true),
		health.Cache(ping, false),
		health.CPU(), health.Memory(),
		clockCheck(now),
	)
}

// clockCheck reports the offset the service is actually running at.
//
// Billing-specific, so it is a local Check: a host without tzdata, or one whose
// tzdata says something else, produces invoices dated a day early for every
// event between 21:00 and midnight, and nothing else would show it. Warn, never
// fail: the instance serves correctly for twenty-one hours a day, and taking it
// out fixes nothing.
func clockCheck(now func() time.Time) health.Check {
	return health.Check{Name: componentClock, Type: health.TypeSystem, Probe: func(context.Context) (health.Observation, error) {
		t := now()
		_, offset := t.In(brcal.Location).Zone()
		obs := health.Observation{Measurement: measureUTCOff, Unit: health.UnitSecond, Value: float64(offset)}
		if brcal.Location.String() != "America/Sao_Paulo" {
			obs.Warn = true
			slog.Warn("health check degraded", "component", componentClock, "location", brcal.Location.String())
		}
		return obs, nil
	}}
}
