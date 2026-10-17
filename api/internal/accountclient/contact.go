package accountclient

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

// ContactScope is the internal scope the contact route sits behind. Its own
// token: the company client above carries another scope, and a token carries one.
const ContactScope = "internal:account:user-contact-read"

// ErrContactNotFound is a user ctech-account has no contact for (unknown, locked
// or purged). An answer, not an outage.
var ErrContactNotFound = errors.New("no contact for this user in ctech-account")

// Contact is one person's verified contact, and nothing else of them.
type Contact struct {
	Email         string `json:"email"`
	FirstName     string `json:"first_name"`
	EmailVerified bool   `json:"email_verified"`
}

// Contacts reads a person's contact from ctech-account
// (GET /v1.0/internal/users/{sub}/contact). A nil *Contacts errors on every call.
type Contacts struct{ c *Client }

// NewContacts returns nil when the credential is incomplete.
func NewContacts(cfg Config) *Contacts {
	c := newWithScope(cfg, ContactScope)
	if c == nil {
		return nil
	}
	return &Contacts{c: c}
}

func (c *Contacts) Contact(ctx context.Context, sub string) (Contact, error) {
	if c == nil {
		return Contact{}, fmt.Errorf("ctech-account contact client is not configured")
	}
	tok, err := c.c.token(ctx)
	if err != nil {
		return Contact{}, err
	}
	var out Contact
	status, err := c.c.getJSON(ctx, tok, fmt.Sprintf("%s/v1.0/internal/users/%s/contact", c.c.baseURL, url.PathEscape(sub)), &out)
	switch {
	case err != nil:
		return Contact{}, err
	case status == http.StatusNotFound:
		return Contact{}, ErrContactNotFound
	case status != http.StatusOK:
		return Contact{}, fmt.Errorf("ctech-account answered %d reading a contact", status)
	}
	return out, nil
}
