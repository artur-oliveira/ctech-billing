package v1

import (
	"bufio"
	"context"
	"log/slog"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/api-commons/cache"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

// The two health endpoints, in the shape every other CTech service uses
// (draft-inadarei-api-health-check, as in ctech-wallet, ctech-poker and
// ctech-dfe).
//
// They answer different questions and that is the whole design:
//
//   - `/v1/health` is liveness. Dependency-free, always 200 while the process
//     is up, and it is what HAProxy probes.
//   - `/v1/health-check` is the dependency report. It can answer 207 or 503, and
//     nothing automated acts on it.
//
// **Billing does not probe the detailed one from the balancer, and the siblings'
// comments about an ALB target group do not transfer.** There is no target group
// here (terraform/README.md): HAProxy probes directly with
// `healthyStatuses = [200]` and `autoHeal = true`, so a status this endpoint
// returns is a status that gets an instance *replaced*. Point the probe here and
// a CPU spike above the warn threshold recycles the fleet, and a DynamoDB outage
// recycles all of it at once — turning a dependency incident into an
// instance-replacement storm on top of it. The report is for a person and for
// monitoring; liveness is for the balancer.

var startTime = time.Now()

// Health check statuses (draft-inadarei-api-health-check).
const (
	statusPass = "pass"
	statusWarn = "warn"
	statusFail = "fail"
)

// statusMultiStatus is what the report returns when billing still serves traffic
// with a degraded dependency.
const statusMultiStatus = 207

// Health check identity.
const (
	healthAPIVersion  = "/v1"
	healthServiceID   = "CTech Billing"
	healthDescription = "Health check details for CTech Billing API"
	// healthUnavailableV is the observedValue of a check that could not be
	// measured at all — distinct from a measured zero.
	healthUnavailableV = -1
)

// Health check component names.
const (
	componentServer   = "server"
	componentCPU      = "cpu"
	componentMemory   = "memory"
	componentDynamoDB = "dynamodb"
	componentCache    = "cache"
	componentClock    = "clock"
)

// Health check component types and measurements.
const (
	typeSystem         = "system"
	typeDatastoreDB    = "datastore"
	typeDatastoreCch   = "datastore:cache"
	measureUptime      = "uptime"
	measureUtilization = "utilization"
	measureResponse    = "responseTime"
	measureUTCOffset   = "utcOffset"
	unitSecond         = "second"
	unitPercent        = "percent"
	unitMillisecond    = "millisecond"
)

const healthCheckTimeout = 2 * time.Second

// utilizationWarnPercent is the CPU/memory level above which the instance
// reports itself degraded.
const utilizationWarnPercent = 90

type healthEntry struct {
	ComponentName   string  `json:"componentName"`
	MeasurementName string  `json:"measurementName"`
	ComponentType   string  `json:"componentType"`
	ObservedValue   float64 `json:"observedValue"`
	ObservedUnit    string  `json:"observedUnit"`
	Status          string  `json:"status"`
	Time            string  `json:"time"`
	// Output is the RFC's free-form field. It carries only non-sensitive
	// descriptors — never the error text from a failed dependency. Both routes
	// are unauthenticated, and "connection refused to <host>:<port>" is a fact
	// about the private network that a stranger has no reason to receive. The
	// error goes to the log, where the operator already is.
	Output string `json:"output,omitempty"`
}

type healthResponse struct {
	Status      string                 `json:"status"`
	Version     string                 `json:"version"`
	ReleaseID   string                 `json:"releaseId"`
	ServiceID   string                 `json:"serviceId"`
	Description string                 `json:"description"`
	Checks      map[string]healthEntry `json:"checks"`
}

// health is the dependency-free liveness probe.
//
// It deliberately checks nothing downstream. A liveness endpoint that fails when
// DynamoDB is slow takes the instance out of rotation during exactly the
// incident when capacity matters most.
//
// `today` and `timezone` are billing's own addition to the house shape, and they
// earn it: a billing service running in UTC bills on the wrong day for three
// hours out of every twenty-four, and this is the cheapest place that would
// show it. They cost one already-loaded clock read.
func (h *handlers) health(c fiber.Ctx) error {
	return c.JSON(fiber.Map{
		"status":    statusPass,
		"releaseId": h.appVersion,
		"serviceId": healthServiceID,
		"today":     h.today().String(),
		"timezone":  brcal.Location.String(),
	})
}

// healthCheck is the dependency report.
//
// DynamoDB is the only check that can fail it. Everything billing does is a
// write to or a read from those tables, so without them there is no degraded
// mode to report — there is nothing. The cache degrades to warn instead: Valkey
// backs the JWKS cache, which has an in-memory fallback, and the settlement bus,
// whose absence makes the payment screen poll rather than be notified. Slower,
// not wrong.
func (h *handlers) healthCheck(c fiber.Ctx) error {
	nowStr := time.Now().UTC().Format(time.RFC3339Nano)
	ctx, cancel := context.WithTimeout(c.Context(), healthCheckTimeout)
	defer cancel()

	checks := map[string]healthEntry{
		measureUptime: {
			ComponentName:   componentServer,
			MeasurementName: measureUptime,
			ComponentType:   typeSystem,
			ObservedValue:   time.Since(startTime).Seconds(),
			ObservedUnit:    unitSecond,
			Status:          statusPass,
			Time:            nowStr,
		},
		componentCPU:      checkCPU(nowStr),
		componentMemory:   checkMemory(nowStr),
		componentDynamoDB: checkDynamoDB(ctx, h.db, h.invoicesTable, nowStr),
		componentCache:    checkCache(ctx, h.cache, nowStr),
		componentClock:    checkClock(h.now(), nowStr),
	}

	overall, statusCode := aggregate(checks)
	return c.Status(statusCode).JSON(healthResponse{
		Status:      overall,
		Version:     healthAPIVersion,
		ReleaseID:   h.appVersion,
		ServiceID:   healthServiceID,
		Description: healthDescription,
		Checks:      checks,
	})
}

// checkDynamoDB describes the one load-bearing table.
//
// DescribeTable rather than ListTables: it is resource-scoped, so it proves both
// connectivity and that this deployment's table actually exists under the
// configured prefix — a `TABLE_PREFIX` typo is otherwise invisible until the
// first real request. ListTables would need account-wide IAM to say less.
func checkDynamoDB(ctx context.Context, db *dynamodb.Client, tableName, nowStr string) healthEntry {
	if db == nil || tableName == "" {
		return healthEntry{componentDynamoDB, measureResponse, typeDatastoreDB, healthUnavailableV, unitMillisecond, statusFail, nowStr, "not configured"}
	}
	started := time.Now()
	_, err := db.DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: aws.String(tableName)})
	latency := float64(time.Since(started).Milliseconds())
	status := statusPass
	if err != nil {
		status = statusFail
		slog.Error("health check failed", "component", componentDynamoDB, "table", tableName, "error", err)
	}
	return healthEntry{componentDynamoDB, measureResponse, typeDatastoreDB, latency, unitMillisecond, status, nowStr, ""}
}

// checkCache pings Valkey. Warn, never fail — see healthCheck.
//
// A nil backend is also warn rather than fail: `newCache` falls back to an
// in-memory backend when RedisURL is unset, which is a real single-instance
// deployment and not a fault.
func checkCache(ctx context.Context, cb cache.Backend, nowStr string) healthEntry {
	if cb == nil {
		return healthEntry{componentCache, measureResponse, typeDatastoreCch, healthUnavailableV, unitMillisecond, statusWarn, nowStr, "not configured"}
	}
	started := time.Now()
	err := cb.Ping(ctx)
	latency := float64(time.Since(started).Milliseconds())
	status := statusPass
	if err != nil {
		status = statusWarn
		slog.Warn("health check degraded", "component", componentCache, "error", err)
	}
	return healthEntry{componentCache, measureResponse, typeDatastoreCch, latency, unitMillisecond, status, nowStr, ""}
}

// checkClock reports the offset the service is actually running at.
//
// It is here and not only on the liveness probe because it is the one check
// whose failure is silent everywhere else: a host without tzdata, or one whose
// tzdata says something else, produces invoices dated a day early for every
// event between 21:00 and midnight. Warn rather than fail — the instance is
// serving correctly for twenty-one hours a day, and taking it out fixes
// nothing.
func checkClock(now time.Time, nowStr string) healthEntry {
	_, offset := now.In(brcal.Location).Zone()
	status := statusPass
	if brcal.Location.String() != "America/Sao_Paulo" {
		status = statusWarn
		slog.Warn("health check degraded", "component", componentClock, "location", brcal.Location.String())
	}
	return healthEntry{
		ComponentName:   componentClock,
		MeasurementName: measureUTCOffset,
		ComponentType:   typeSystem,
		ObservedValue:   float64(offset),
		ObservedUnit:    unitSecond,
		Status:          status,
		Time:            nowStr,
		Output:          brcal.FromTime(now).String() + " " + brcal.Location.String(),
	}
}

// aggregate reduces the checks to one status and one HTTP code: any fail → 503,
// else any warn → 207, else 200.
func aggregate(checks map[string]healthEntry) (string, int) {
	overall := statusPass
	for _, e := range checks {
		if e.Status == statusFail {
			return statusFail, fiber.StatusServiceUnavailable
		}
		if e.Status == statusWarn {
			overall = statusWarn
		}
	}
	if overall == statusWarn {
		return statusWarn, statusMultiStatus
	}
	return statusPass, fiber.StatusOK
}

func checkCPU(nowStr string) healthEntry {
	return utilization(componentCPU, cpuPercent(), nowStr)
}

func checkMemory(nowStr string) healthEntry {
	return utilization(componentMemory, memoryPercent(), nowStr)
}

// utilization turns a percentage into an entry. An unmeasurable value is warn,
// not pass: "we could not tell" and "it is fine" are different answers, and
// reporting the second for the first is how a saturated instance looks healthy.
func utilization(component string, pct float64, nowStr string) healthEntry {
	status := statusPass
	if pct < 0 || pct > utilizationWarnPercent {
		status = statusWarn
	}
	return healthEntry{component, measureUtilization, typeSystem, pct, unitPercent, status, nowStr, ""}
}

// cpuPercent reads /proc/stat. It is the utilisation since boot rather than an
// instantaneous sample, which is what a single read can honestly give; a rate
// would need two reads and a sleep inside a health check.
func cpuPercent() float64 {
	if runtime.GOOS != "linux" {
		return healthUnavailableV
	}
	f, err := os.Open("/proc/stat")
	if err != nil {
		return healthUnavailableV
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		return healthUnavailableV
	}
	fields := strings.Fields(scanner.Text())
	if len(fields) < 5 || fields[0] != "cpu" {
		return healthUnavailableV
	}
	var vals []int64
	for _, s := range fields[1:] {
		v, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			break
		}
		vals = append(vals, v)
	}
	if len(vals) < 4 {
		return healthUnavailableV
	}
	idle := vals[3]
	var total int64
	for _, v := range vals {
		total += v
	}
	if total == 0 {
		return healthUnavailableV
	}
	return roundOne(100.0 * float64(total-idle) / float64(total))
}

// memoryPercent uses MemAvailable rather than MemFree. MemFree excludes the page
// cache, which Linux fills on purpose, so a healthy instance reads as 95% used.
func memoryPercent() float64 {
	if runtime.GOOS != "linux" {
		return healthUnavailableV
	}
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return healthUnavailableV
	}
	defer func() { _ = f.Close() }()
	info := map[string]int64{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		key, rest, ok := strings.Cut(scanner.Text(), ":")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		if v, err := strconv.ParseInt(fields[0], 10, 64); err == nil {
			info[strings.TrimSpace(key)] = v
		}
	}
	total, ok1 := info["MemTotal"]
	available, ok2 := info["MemAvailable"]
	if !ok1 || !ok2 || total == 0 {
		return healthUnavailableV
	}
	return roundOne(100.0 * float64(total-available) / float64(total))
}

func roundOne(v float64) float64 {
	return float64(int64(v*10+0.5)) / 10
}
