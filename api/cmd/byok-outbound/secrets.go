package main

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

type paramReader interface {
	GetParameter(ctx context.Context, in *ssm.GetParameterInput, opts ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
}

func readParam(ctx context.Context, r paramReader, name string) (string, error) {
	out, err := r.GetParameter(ctx, &ssm.GetParameterInput{Name: aws.String(name), WithDecryption: aws.Bool(true)})
	if err != nil {
		return "", fmt.Errorf("ssm %s: unreadable", name) // never the value, never the SDK error body
	}
	return aws.ToString(out.Parameter.Value), nil
}

// sandboxLoader reads CTech's own Inter integration (the wallet's parameters,
// by IAM grant: spec amendment 2026-10-11) once per container, on first use.
func sandboxLoader(r paramReader, credentialParams, accountParam, pixKeyParam string) func(context.Context) (sandboxCreds, error) {
	var once sync.Once
	var creds sandboxCreds
	var loadErr error
	return func(ctx context.Context) (sandboxCreds, error) {
		once.Do(func() {
			values := map[string]string{}
			names := map[string]string{"client-id": "client_id", "client-secret": "client_secret", "cert": "cert_pem", "key": "key_pem"}
			for _, pair := range strings.Split(credentialParams, ",") {
				k, path, _ := strings.Cut(pair, "=")
				v, err := readParam(ctx, r, path)
				if err != nil {
					loadErr = err
					return
				}
				values[names[k]] = v
			}
			acct, err := readParam(ctx, r, accountParam)
			if err != nil {
				loadErr = err
				return
			}
			pix, err := readParam(ctx, r, pixKeyParam)
			if err != nil {
				loadErr = err
				return
			}
			creds = sandboxCreds{values: values, account: acct, pixKey: pix}
		})
		return creds, loadErr
	}
}
