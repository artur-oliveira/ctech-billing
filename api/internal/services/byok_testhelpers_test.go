package services

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/lambda"

	"gopkg.aoctech.app/billing/api/internal/byokrpc"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// countingInvoker answers nothing useful: the tests here are refusals that must
// happen before any Lambda call, so the count is what they read.
type countingInvoker struct{ n atomic.Int32 }

func (c *countingInvoker) Invoke(context.Context, *lambda.InvokeInput, ...func(*lambda.Options)) (*lambda.InvokeOutput, error) {
	c.n.Add(1)
	return &lambda.InvokeOutput{StatusCode: 200, Payload: []byte(`{"code":"unavailable","error":"unavailable"}`)}, nil
}

func (c *countingInvoker) calls() int { return int(c.n.Load()) }

// Repositories are nil: every refusal tested here happens before any read.
func newConnectionsForTest(t *testing.T, inv *countingInvoker) *ByokConnections {
	t.Helper()
	return NewByokConnections(nil, nil, byokrpc.NewOutbound(inv, "fn"), nil, nil, nil)
}

func pair(t *testing.T, notAfter time.Time) (certPEM, keyPEM string) {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "integration"},
		NotBefore: time.Now().Add(-48 * time.Hour), NotAfter: notAfter, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	kb, _ := x509.MarshalECPrivateKey(k)
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}))
}

func validInput(t *testing.T) ConnectInput {
	c, k := pair(t, time.Now().AddDate(1, 0, 0))
	return ConnectInput{Provider: "inter", ClientID: "cid", ClientSecret: "secret", CertPEM: c, KeyPEM: k,
		Account: "1234567", NotifyEmail: "financeiro@example.com", BackfillMonths: 3, Authorized: true}
}

func otherKeyPEM(t *testing.T) string { _, k := pair(t, time.Now().AddDate(1, 0, 0)); return k }

func expiredPair(t *testing.T) (string, string) { return pair(t, time.Now().Add(-time.Hour)) }

func orgSpace(t *testing.T) space.ResolvedSpace {
	sp, err := space.ForJob("01a04ed6-1af9-745e-bcf7-d3b66fe52321", true)
	if err != nil {
		t.Fatal(err)
	}
	return sp
}

func personalSpace(t *testing.T) space.ResolvedSpace {
	sp, err := space.ForInvoicePayer("sub-1", true)
	if err != nil {
		t.Fatal(err)
	}
	return sp
}
