package invoicepdf

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"gopkg.aoctech.app/api-commons/awsconfig"
)

// Store holds rendered invoices in S3.
//
// **Write-once.** A stored object is never replaced: the document was handed to
// a customer, and a system that can silently re-render what somebody already
// filed is a system whose documents cannot be relied on. Every put is
// conditional on the key not existing, and a losing race is not an error — the
// other writer produced the same bytes, because Render is a function of frozen
// facts.
//
// The object is served by a presigned URL rather than proxied through the API.
// A PDF is a few kilobytes but the API instances are t4g.nano behind one shared
// edge, and a download path through them is a download path that competes with
// paying an invoice.
type Store struct {
	s3      *s3.Client
	presign *s3.PresignClient
	bucket  string
}

// ErrNotConfigured reports a deployment with no bucket. Callers turn it into a
// route that is simply not mounted rather than one that fails at the moment
// somebody clicks download.
var ErrNotConfigured = errors.New("invoice PDF storage is not configured")

// New builds a store. An empty bucket yields a nil store, which Enabled reports
// — the same shape the wallet and payment-link configuration already use.
func New(ctx context.Context, region, bucket string) (*Store, error) {
	if bucket == "" {
		return nil, nil
	}
	cfg, err := awsconfig.Load(ctx, region)
	if err != nil {
		return nil, fmt.Errorf("invoicepdf: loading AWS config: %w", err)
	}
	client := s3.NewFromConfig(cfg)
	return &Store{s3: client, presign: s3.NewPresignClient(client), bucket: bucket}, nil
}

// Enabled reports a store that can actually serve documents.
func (s *Store) Enabled() bool { return s != nil && s.bucket != "" }

// Key is where one invoice's document lives.
//
// Tenant and mode first, so a bucket policy or a lifecycle rule can address one
// organization's documents without parsing an id — and so a listing of the
// prefix is a listing of that tenant, never of everybody's.
func Key(organizationID string, livemode bool, invoiceID string) string {
	mode := "test"
	if livemode {
		mode = "live"
	}
	return fmt.Sprintf("invoices/%s/%s/%s.pdf", organizationID, mode, invoiceID)
}

// Put stores the document, unless it is already there.
//
// The condition is the whole point: two browsers downloading the same invoice
// at once both render it, and exactly one write survives. The loser is told so
// and does nothing, because the object that won is byte-identical.
func (s *Store) Put(ctx context.Context, key string, body []byte) error {
	if !s.Enabled() {
		return ErrNotConfigured
	}
	_, err := s.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(body),
		ContentType: aws.String("application/pdf"),
		IfNoneMatch: aws.String("*"),
	})
	if err != nil && isPreconditionFailed(err) {
		// Somebody stored it first. Identical bytes, so there is nothing to do.
		return nil
	}
	return err
}

// Exists reports whether the document has already been rendered.
func (s *Store) Exists(ctx context.Context, key string) (bool, error) {
	if !s.Enabled() {
		return false, ErrNotConfigured
	}
	_, err := s.s3.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err == nil {
		return true, nil
	}
	var missing *types.NotFound
	if errors.As(err, &missing) {
		return false, nil
	}
	return false, err
}

// DownloadTTL is how long a link to a document lives.
//
// Five minutes: long enough to survive a slow phone and a tap that was not
// immediate, short enough that a URL copied out of a browser's history is not a
// standing grant to somebody else's invoice. The link carries no
// authentication of its own, which is exactly why it must not outlive the click
// that produced it.
const DownloadTTL = 5 * time.Minute

// DownloadURL signs a link to the stored document.
//
// `response-content-disposition` is set so the browser saves it as
// "fatura-1042.pdf" rather than the object key, which is a ULID nobody can
// file.
func (s *Store) DownloadURL(ctx context.Context, key, filename string) (string, error) {
	if !s.Enabled() {
		return "", ErrNotConfigured
	}
	req, err := s.presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket:                     aws.String(s.bucket),
		Key:                        aws.String(key),
		ResponseContentType:        aws.String("application/pdf"),
		ResponseContentDisposition: aws.String(`attachment; filename="` + filename + `"`),
	}, s3.WithPresignExpires(DownloadTTL))
	if err != nil {
		return "", fmt.Errorf("invoicepdf: signing the download: %w", err)
	}
	return req.URL, nil
}

// isPreconditionFailed reports the conditional-put refusal that means the
// object already exists.
func isPreconditionFailed(err error) bool {
	var api interface{ ErrorCode() string }
	if errors.As(err, &api) {
		return api.ErrorCode() == "PreconditionFailed"
	}
	return false
}
