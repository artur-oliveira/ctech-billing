//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

type historyDelivery struct {
	EndpointID     string `json:"endpoint_id"`
	EndpointURL    string `json:"endpoint_url"`
	Status         string `json:"status"`
	Attempts       int    `json:"attempts"`
	LastStatusCode int    `json:"last_status_code"`
	AttemptLog     []struct {
		Number     int    `json:"number"`
		StatusCode int    `json:"status_code"`
		Error      string `json:"error"`
		Response   string `json:"response"`
	} `json:"attempt_log"`
}

type historyEvent struct {
	ID             string            `json:"id"`
	Type           string            `json:"type"`
	Object         string            `json:"object"`
	ObjectID       string            `json:"object_id"`
	SubscriptionID string            `json:"subscription_id"`
	Deliveries     []historyDelivery `json:"deliveries"`
	Payload        json.RawMessage   `json:"payload"`
}

type historyList struct {
	Data    []historyEvent `json:"data"`
	HasMore bool           `json:"has_more"`
	Cursor  string         `json:"cursor"`
}

func TestAnInvoicesHistoryShowsItsEventsAndEveryAttemptToEveryEndpoint(t *testing.T) {
	e := newAPI(t)
	cons := newConsumer(t, http.StatusUnauthorized) // refuses the first attempts
	ep := registerEndpoint(t, e.org, cons, "")

	tok := e.oneOffToken(t)
	cust := e.customerID(t, tok)
	r := e.do(t, http.MethodPost, "/v1.0/invoices", tok, "k-history", `{"customer_id":"`+cust+`","description":"Depósito","amount_cents":5000,"reference":"hist-1"}`)
	if r.status != http.StatusCreated {
		t.Fatalf("%d %s", r.status, r.body)
	}
	var made oneOffInvoice
	r.decode(t, &made)

	d := newDeliverer(t, cons)
	runDelivery(t, d, e.org, time.Now())

	get := func(path string) historyList {
		res := e.do(t, http.MethodGet, path, tok, "", "")
		if res.status != http.StatusOK {
			t.Fatalf("%s: %d %s", path, res.status, res.body)
		}
		var list historyList
		res.decode(t, &list)
		return list
	}
	list := get("/v1.0/invoices/" + made.ID + "/events?include=attempts")
	types := map[string]bool{}
	for _, ev := range list.Data {
		types[ev.Type] = true
		if ev.Object != "invoice" || ev.ObjectID != made.ID {
			t.Fatalf("an event of something else: %+v", ev)
		}
	}
	if !types["invoice.finalized"] {
		t.Fatalf("events %v", types) // a one-off invoice is announced when it is finalized
	}
	for _, ev := range list.Data {
		if len(ev.Deliveries) != 1 {
			t.Fatalf("%s deliveries %+v", ev.Type, ev.Deliveries)
		}
		dv := ev.Deliveries[0]
		if dv.EndpointID != ep.ID || dv.Status != "pending" || dv.Attempts != 1 || dv.LastStatusCode != 401 || len(dv.AttemptLog) != 1 || dv.AttemptLog[0].StatusCode != 401 {
			t.Fatalf("after one refused attempt: %+v", dv)
		}
		if !strings.HasPrefix(dv.EndpointURL, "https://") || strings.Contains(dv.EndpointURL, "?") {
			t.Fatalf("endpoint url %q", dv.EndpointURL)
		}
	}

	// The endpoint recovers; the retry (after the backoff) delivers, and the log keeps both tries.
	cons.setCode(http.StatusOK)
	runDelivery(t, d, e.org, time.Now().Add(2*time.Minute))
	list = get("/v1.0/invoices/" + made.ID + "/events?include=attempts")
	for _, ev := range list.Data {
		dv := ev.Deliveries[0]
		if dv.Status != "delivered" || dv.Attempts != 2 || len(dv.AttemptLog) != 2 || dv.AttemptLog[0].StatusCode != 401 || dv.AttemptLog[1].StatusCode != 200 {
			t.Fatalf("after the retry: %+v", dv)
		}
	}
	// Without include=attempts the log is left out.
	if got := get("/v1.0/invoices/" + made.ID + "/events"); len(got.Data[0].Deliveries[0].AttemptLog) != 0 {
		t.Fatalf("the log is opt-in: %+v", got.Data[0].Deliveries[0])
	}

	// The cursor is accepted and a short history ends without one.
	if all := get("/v1.0/invoices/" + made.ID + "/events?limit=1"); len(all.Data) != 1 || all.HasMore {
		t.Fatalf("one event fits one page: %+v", all)
	}

	// Refusals.
	if res := e.do(t, http.MethodGet, "/v1.0/invoices/in_nope/events", tok, "", ""); res.status != http.StatusNotFound {
		t.Fatalf("an unknown invoice: %d", res.status)
	}
	if res := e.do(t, http.MethodGet, "/v1.0/invoices/"+made.ID+"/events?limit=500", tok, "", ""); res.status != http.StatusUnprocessableEntity && res.status != http.StatusBadRequest {
		t.Fatalf("a page that is too large: %d", res.status)
	}
	if res := e.do(t, http.MethodGet, "/v1.0/invoices/"+made.ID+"/events?cursor=not-a-cursor", tok, "", ""); res.status != http.StatusBadRequest {
		t.Fatalf("a bad cursor: %d", res.status)
	}
	noScope := e.token(t, e.client, "", middleware.ScopeCustomersRead)
	if res := e.do(t, http.MethodGet, "/v1.0/invoices/"+made.ID+"/events", noScope, "", ""); res.status != http.StatusForbidden {
		t.Fatalf("without invoices:read: %d", res.status)
	}
}

func TestACredentialScopedToAnOwnerSeesOnlyItsOwnersEvents(t *testing.T) {
	e := newAPI(t)
	tok := e.oneOffToken(t)
	cust := e.customerID(t, tok)
	ownedBody := `{"customer_id":"` + cust + `","description":"x","amount_cents":5000,"reference":"hist-own","owner_key":"wallet"}`
	r := e.do(t, http.MethodPost, "/v1.0/invoices", tok, "k-own", ownedBody)
	var made oneOffInvoice
	r.decode(t, &made)

	// An unscoped credential sees the invoice's events whatever the owner...
	res := e.do(t, http.MethodGet, "/v1.0/invoices/"+made.ID+"/events", tok, "", "")
	var list historyList
	res.decode(t, &list)
	if res.status != http.StatusOK || len(list.Data) == 0 {
		t.Fatalf("%d %s", res.status, res.body)
	}
	// ... a credential scoped to another owner sees none of them.
	creds := repositories.NewCredentialRepository(testDB, testCfg)
	cred, err := creds.Resolve(ctxT(t), e.client)
	if err != nil {
		t.Fatal(err)
	}
	if err := creds.SetOwnerKey(ctxT(t), cred, "poker", time.Now()); err != nil {
		t.Fatal(err)
	}
	res = e.do(t, http.MethodGet, "/v1.0/invoices/"+made.ID+"/events", tok, "", "")
	list = historyList{}
	res.decode(t, &list)
	if res.status != http.StatusOK || len(list.Data) != 0 {
		t.Fatalf("another owner's events leaked: %d %s", res.status, res.body)
	}
}

func TestAnEndpointsHistoryIsPagedNewestFirstWithItsAttempts(t *testing.T) {
	e := newAPI(t)
	tok := e.writeToken(t)
	cons := newConsumer(t, http.StatusOK)
	ep := registerEndpoint(t, e.org, cons, "")

	m2m := e.oneOffToken(t)
	cust := e.customerID(t, m2m)
	for i, ref := range []string{"hist-ep-1", "hist-ep-2", "hist-ep-3", "hist-ep-4", "hist-ep-5"} {
		r := e.do(t, http.MethodPost, "/v1.0/invoices", m2m, "k-ep-"+ref, `{"customer_id":"`+cust+`","description":"x","amount_cents":5000,"reference":"`+ref+`"}`)
		if r.status != http.StatusCreated {
			t.Fatalf("%d: %d %s", i, r.status, r.body)
		}
	}
	runDelivery(t, newDeliverer(t, cons), e.org, time.Now())

	path := hooksPath + "/" + ep.ID + "/deliveries"
	res := e.console(t, path+"?limit=3&include=attempts", tok, "live")
	if res.status != http.StatusOK {
		t.Fatalf("%d %s", res.status, res.body)
	}
	var page struct {
		Data []struct {
			EventID  string `json:"event_id"`
			Type     string `json:"type"`
			Delivery struct {
				Status     string `json:"status"`
				AttemptLog []struct {
					StatusCode int `json:"status_code"`
				} `json:"attempt_log"`
			} `json:"delivery"`
		} `json:"data"`
		HasMore bool   `json:"has_more"`
		Cursor  string `json:"cursor"`
	}
	res.decode(t, &page)
	if len(page.Data) != 3 || !page.HasMore || page.Cursor == "" {
		t.Fatalf("page %+v", page)
	}
	for _, d := range page.Data {
		if d.Delivery.Status != "delivered" || len(d.Delivery.AttemptLog) != 1 || d.Delivery.AttemptLog[0].StatusCode != 200 {
			t.Fatalf("%+v", d)
		}
	}
	// Newest first: event ids are time-ordered, so they descend.
	for i := 1; i < len(page.Data); i++ {
		if page.Data[i-1].EventID < page.Data[i].EventID {
			t.Fatalf("not newest first: %s before %s", page.Data[i-1].EventID, page.Data[i].EventID)
		}
	}
	next := e.console(t, path+"?limit=3&cursor="+page.Cursor, tok, "live")
	var more struct {
		Data []struct {
			EventID string `json:"event_id"`
		} `json:"data"`
	}
	next.decode(t, &more)
	if next.status != http.StatusOK || len(more.Data) == 0 || more.Data[0].EventID == page.Data[2].EventID {
		t.Fatalf("second page %d %s", next.status, next.body)
	}
	if res := e.console(t, hooksPath+"/whe_nope/deliveries", tok, "live"); res.status != http.StatusNotFound {
		t.Fatalf("an unknown endpoint: %d", res.status)
	}

	// One event, in full: its payload, its deliveries and every attempt.
	detail := e.console(t, "/v1.0/console/events/"+page.Data[0].EventID, tok, "live")
	var one historyEvent
	detail.decode(t, &one)
	if detail.status != http.StatusOK || one.ID != page.Data[0].EventID || len(one.Payload) == 0 || len(one.Deliveries) != 1 || len(one.Deliveries[0].AttemptLog) != 1 {
		t.Fatalf("detail %d %s", detail.status, detail.body)
	}
}

func TestASubscriptionsHistoryIncludesTheEventsOfItsInvoices(t *testing.T) {
	e := newAPI(t)
	price := seedProduct(t, e.org, "prod_hist_sub", "")
	sub := subscribe(t, e.org, price)

	tok := e.token(t, e.client, "", middleware.ScopeSubscriptionsRead)
	res := e.do(t, http.MethodGet, "/v1.0/subscriptions/"+sub.ID+"/events", tok, "", "")
	if res.status != http.StatusOK {
		t.Fatalf("%d %s", res.status, res.body)
	}
	var list historyList
	res.decode(t, &list)
	objects := map[string]bool{}
	for _, ev := range list.Data {
		objects[ev.Object] = true
		if ev.SubscriptionID != sub.ID {
			t.Fatalf("an event of another subscription: %+v", ev)
		}
	}
	if !objects["invoice"] {
		t.Fatalf("the events of the invoices a subscription produced belong to its history: %v", objects)
	}
	if res := e.do(t, http.MethodGet, "/v1.0/subscriptions/sub_nope/events", tok, "", ""); res.status != http.StatusNotFound {
		t.Fatalf("an unknown subscription: %d", res.status)
	}
}

// Events written before the pointers existed are found again by the backfill, and
// running it twice writes nothing the second time.
func TestTheBackfillRestoresThePointersOfOlderEvents(t *testing.T) {
	e := newAPI(t)
	cons := newConsumer(t, http.StatusOK)
	ep := registerEndpoint(t, e.org, cons, "")
	tok := e.oneOffToken(t)
	cust := e.customerID(t, tok)
	r := e.do(t, http.MethodPost, "/v1.0/invoices", tok, "k-backfill", `{"customer_id":"`+cust+`","description":"x","amount_cents":5000,"reference":"hist-backfill"}`)
	var made oneOffInvoice
	r.decode(t, &made)
	runDelivery(t, newDeliverer(t, cons), e.org, time.Now())

	hooks := repositories.NewWebhookRepository(testDB, testCfg)
	page, err := hooks.EventsOf(ctxT(t), e.org.ID, true, "invoice", made.ID, nil, 10)
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("before: %v %+v", err, page)
	}
	evID := page.Items[0].ID
	del := func(sk string) {
		_, err := testDB.DeleteItem(ctxT(t), &dynamodb.DeleteItemInput{
			TableName: aws.String(repositories.TableName(testCfg, repositories.TableWebhooks)),
			Key: map[string]types.AttributeValue{
				"pk": &types.AttributeValueMemberS{Value: repositories.TenantPK(e.org.ID, true)},
				"sk": &types.AttributeValueMemberS{Value: sk},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	del(repositories.EntityEventSK("invoice", made.ID, evID))
	del(repositories.EndpointEventSK(ep.ID, evID))
	if p, _ := hooks.EventsOf(ctxT(t), e.org.ID, true, "invoice", made.ID, nil, 10); len(p.Items) != 0 {
		t.Fatal("the pointer is gone, the event is not findable from the invoice")
	}

	if rep, err := hooks.BackfillHistoryLookups(ctxT(t), false); err != nil || rep.Written != 0 || rep.Events == 0 {
		t.Fatalf("a dry run only counts: %+v %v", rep, err)
	}
	if rep, err := hooks.BackfillHistoryLookups(ctxT(t), true); err != nil || rep.Written < 2 {
		t.Fatalf("apply: %+v %v", rep, err)
	}
	if p, _ := hooks.EventsOf(ctxT(t), e.org.ID, true, "invoice", made.ID, nil, 10); len(p.Items) != 1 || p.Items[0].ID != evID {
		t.Fatalf("after: %+v", p)
	}
	if p, _ := hooks.DeliveriesOfEndpoint(ctxT(t), e.org.ID, true, ep.ID, nil, 10); len(p.Items) != 1 {
		t.Fatalf("the endpoint's history after: %+v", p)
	}
	if rep, err := hooks.BackfillHistoryLookups(ctxT(t), true); err != nil || rep.Written != 0 {
		t.Fatalf("a second run writes nothing: %+v %v", rep, err)
	}
}
