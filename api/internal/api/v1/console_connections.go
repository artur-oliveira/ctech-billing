package v1

import (
	"errors"
	"io"
	"mime/multipart"
	"slices"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/problem"
	"gopkg.aoctech.app/billing/api/internal/services"
	"gopkg.aoctech.app/billing/api/internal/space"
)

const maxPEMBytes = 16 << 10

type connectionHandlers struct {
	conns     *services.ByokConnections
	statement *services.ByokStatement
	clock     func() time.Time
}

func registerConnections(v1 fiber.Router, d Deps, auth fiber.Handler, clock func() time.Time) {
	if d.ByokConnections == nil || d.Spaces == nil {
		return
	}
	h := &connectionHandlers{conns: d.ByokConnections, statement: d.ByokStatement, clock: clock}
	idem := middleware.SpaceIdempotency(d.Idempotency, clock)
	g := v1.Group("/console/connections", auth)
	chain := func(write bool) []any {
		scope := middleware.ScopeFinanceRead
		if write {
			scope = middleware.ScopeFinanceWrite
		}
		c := []any{middleware.RequireUserScope(scope), middleware.ResolveSpace(d.Spaces), middleware.RequireVerb(space.Configure)}
		if write {
			c = append(c, idem)
		}
		return c
	}
	add := func(method, path string, write bool, fn fiber.Handler) {
		c := append(chain(write), knownProvider, fn)
		g.Add([]string{method}, path, c[0], c[1:]...)
	}
	g.Add([]string{"GET"}, "", chain(false)[0], append(chain(false)[1:], h.list)...)
	add("GET", "/:provider", false, h.get)
	add("PUT", "/:provider", true, h.connect)
	add("DELETE", "/:provider", true, h.disconnect)
	add("POST", "/:provider/test", true, h.test)
	add("POST", "/:provider/certificate", true, h.renew)
	add("POST", "/:provider/sync", true, h.sync)
	add("PUT", "/:provider/onboarding", true, h.onboarding)
}

func knownProvider(c fiber.Ctx) error {
	if p := c.Params("provider"); p != "" && !slices.Contains(services.KnownProviders, p) {
		return problem.NotFound("resource not found").WithCode("resource_not_found").Send(c)
	}
	return c.Next()
}

func (h *connectionHandlers) now() time.Time { return h.clock() }

func (h *connectionHandlers) list(c fiber.Ctx) error {
	sp := middleware.GetSpace(c)
	conns, err := h.conns.List(c.Context(), sp)
	if err != nil {
		return fail(c, err)
	}
	out := make([]connectionDTO, len(conns))
	for i := range conns {
		out[i] = toConnectionDTO(&conns[i], h.now())
	}
	return c.JSON(fiber.Map{"data": out, "providers": providerCatalog()})
}

func (h *connectionHandlers) get(c fiber.Ctx) error {
	conn, err := h.conns.Get(c.Context(), middleware.GetSpace(c), c.Params("provider"))
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(toConnectionDTO(conn, h.now()))
}

func readPEM(fh *multipart.FileHeader) (string, error) {
	if fh == nil || fh.Size > maxPEMBytes {
		return "", errors.New("file missing or larger than 16 KiB")
	}
	f, err := fh.Open()
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, maxPEMBytes))
	return string(b), err
}

func (h *connectionHandlers) connect(c fiber.Ctx) error {
	sp := middleware.GetSpace(c)
	actor, req := middleware.GetClaims(c).Sub, middleware.GetRequestID(c)
	provider := c.Params("provider")
	if !sp.Livemode() {
		conn, err := h.conns.EnableSandbox(c.Context(), sp, provider, actor, req, h.now())
		if err != nil {
			return fail(c, err)
		}
		return c.Status(fiber.StatusCreated).JSON(toConnectionDTO(conn, h.now()))
	}
	form, err := c.MultipartForm()
	if err != nil {
		return problem.Validation([]problem.FieldError{problem.Field("cert", "required", "multipart form with cert and key files")}).Send(c)
	}
	field := func(k string) string {
		if v := form.Value[k]; len(v) > 0 {
			return v[0]
		}
		return ""
	}
	file := func(k string) *multipart.FileHeader {
		if v := form.File[k]; len(v) > 0 {
			return v[0]
		}
		return nil
	}
	cert, cerr := readPEM(file("cert"))
	key, kerr := readPEM(file("key"))
	if cerr != nil || kerr != nil {
		return problem.Validation([]problem.FieldError{problem.Field("cert", "invalid_file", "cert and key are PEM files up to 16 KiB")}).Send(c)
	}
	months, _ := strconv.Atoi(field("backfill_months"))
	conn, err := h.conns.Connect(c.Context(), sp, services.ConnectInput{
		Provider: provider, ClientID: field("client_id"), ClientSecret: field("client_secret"), CertPEM: cert, KeyPEM: key,
		Account: field("account"), PixKey: field("pix_key"), NotifyEmail: field("notify_email"),
		BackfillMonths: months, Authorized: field("authorized") == "true",
	}, actor, req, h.now())
	if err != nil {
		return fail(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(toConnectionDTO(conn, h.now()))
}

func (h *connectionHandlers) test(c fiber.Ctx) error {
	conn, err := h.conns.Test(c.Context(), middleware.GetSpace(c), c.Params("provider"), h.now())
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(toConnectionDTO(conn, h.now()))
}

func (h *connectionHandlers) renew(c fiber.Ctx) error {
	cfh, _ := c.FormFile("cert")
	kfh, _ := c.FormFile("key")
	cert, cerr := readPEM(cfh)
	key, kerr := readPEM(kfh)
	if cerr != nil || kerr != nil {
		return problem.Validation([]problem.FieldError{problem.Field("cert", "invalid_file", "cert and key are PEM files up to 16 KiB")}).Send(c)
	}
	conn, err := h.conns.Renew(c.Context(), middleware.GetSpace(c), c.Params("provider"), cert, key,
		middleware.GetClaims(c).Sub, middleware.GetRequestID(c), h.now())
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(toConnectionDTO(conn, h.now()))
}

func (h *connectionHandlers) sync(c fiber.Ctx) error {
	sp := middleware.GetSpace(c)
	conn, err := h.conns.Connected(c.Context(), sp, c.Params("provider"))
	if err != nil {
		return fail(c, err)
	}
	res, err := h.statement.SyncNow(c.Context(), sp, conn, h.now())
	if err != nil {
		return fail(c, err)
	}
	return c.Status(fiber.StatusAccepted).JSON(fiber.Map{"posted": res.Posted, "skipped": res.Skipped, "backfill_done": res.BackfillDone})
}

func (h *connectionHandlers) onboarding(c fiber.Ctx) error {
	var body struct {
		Step string `json:"step"`
	}
	if p := decodeStrict(c, &body); p != nil {
		return p.Send(c)
	}
	if err := h.conns.SetOnboarding(c.Context(), middleware.GetSpace(c), c.Params("provider"), body.Step, h.now()); err != nil {
		return fail(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (h *connectionHandlers) disconnect(c fiber.Ctx) error {
	if err := h.conns.Disconnect(c.Context(), middleware.GetSpace(c), c.Params("provider"),
		middleware.GetClaims(c).Sub, middleware.GetRequestID(c), h.now()); err != nil {
		return fail(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}
