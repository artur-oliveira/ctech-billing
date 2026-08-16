package repositories

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// schema.json is the **only** definition of the tables. Go embeds it here and
// `terraform/billing/dynamodb.tf` reads it with jsondecode(file(...)).
//
// It used to live twice — a Go CreateTableInput for the integration tests and a
// Terraform resource for the real thing — with a test that parsed the .tf with
// regexes to prove the two agreed. That test worked, and it was answering a
// question that should not exist: two definitions of one schema drift, and the
// way that drift is found is a query that passes every test and fails in
// production. One file read by both readers cannot drift at all.
//
//go:embed schema.json
var schemaJSON []byte

// TableSchema is one table's key layout. Only key attributes appear — every
// other attribute on a row is schemaless, and DynamoDB rejects an attribute
// definition no index uses.
type TableSchema struct {
	HashKey  string `json:"hash_key"`
	RangeKey string `json:"range_key,omitempty"`
	// Attributes maps a key attribute's name to its DynamoDB type. Every key
	// attribute in this service is a string, which is what makes a page cursor a
	// small string map (see EncodeCursor).
	Attributes map[string]string `json:"attributes"`
	Indexes    []IndexSchema     `json:"indexes"`
}

// IndexSchema is one global secondary index. Every index projects ALL: the
// alternative is a projection that has to be widened later, and widening a
// projection means rebuilding the index on a live table.
type IndexSchema struct {
	Name     string `json:"name"`
	HashKey  string `json:"hash_key"`
	RangeKey string `json:"range_key,omitempty"`
}

var schemas = func() map[string]TableSchema {
	var s map[string]TableSchema
	if err := json.Unmarshal(schemaJSON, &s); err != nil {
		// Unreachable: the file is embedded at build time, so a malformed one
		// fails every test on the machine that broke it.
		panic("repositories: schema.json is not valid JSON: " + err.Error())
	}
	return s
}()

// Schemas returns the table layout, keyed by logical table name.
func Schemas() map[string]TableSchema { return schemas }

// TableNames returns every logical table name, sorted — the order matters only
// so that logs and test output are stable.
func TableNames() []string {
	out := make([]string, 0, len(schemas))
	for name := range schemas {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// TableDefinition renders one logical table as a CreateTableInput, for the
// integration tests and local development. Production schema is Terraform's,
// reading the same JSON.
func TableDefinition(logical, physicalName string) (*dynamodb.CreateTableInput, error) {
	s, ok := schemas[logical]
	if !ok {
		return nil, fmt.Errorf("repositories: no table %q in schema.json", logical)
	}

	attrs := make([]types.AttributeDefinition, 0, len(s.Attributes))
	for _, name := range sortedAttributeNames(s.Attributes) {
		attrs = append(attrs, types.AttributeDefinition{
			AttributeName: aws.String(name),
			AttributeType: types.ScalarAttributeType(s.Attributes[name]),
		})
	}

	gsis := make([]types.GlobalSecondaryIndex, 0, len(s.Indexes))
	for _, idx := range s.Indexes {
		gsis = append(gsis, types.GlobalSecondaryIndex{
			IndexName:  aws.String(idx.Name),
			KeySchema:  keySchema(idx.HashKey, idx.RangeKey),
			Projection: &types.Projection{ProjectionType: types.ProjectionTypeAll},
		})
	}

	in := &dynamodb.CreateTableInput{
		TableName:            aws.String(physicalName),
		AttributeDefinitions: attrs,
		KeySchema:            keySchema(s.HashKey, s.RangeKey),
		BillingMode:          types.BillingModePayPerRequest,
	}
	if len(gsis) > 0 {
		in.GlobalSecondaryIndexes = gsis
	}
	return in, nil
}

func keySchema(hash, rng string) []types.KeySchemaElement {
	out := []types.KeySchemaElement{
		{AttributeName: aws.String(hash), KeyType: types.KeyTypeHash},
	}
	if rng != "" {
		out = append(out, types.KeySchemaElement{
			AttributeName: aws.String(rng), KeyType: types.KeyTypeRange,
		})
	}
	return out
}

func sortedAttributeNames(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// EnsureTables creates every missing table.
//
// For tests and local development only. Production schema is owned by
// Terraform: a service that creates its own tables is a service that can create
// the wrong one and then quietly work against it.
func EnsureTables(ctx context.Context, db *dynamodb.Client, tablePrefix string) error {
	for _, logical := range TableNames() {
		in, err := TableDefinition(logical, PhysicalName(tablePrefix, logical))
		if err != nil {
			return err
		}
		_, err = db.CreateTable(ctx, in)
		var exists *types.ResourceInUseException
		if err != nil && !errors.As(err, &exists) {
			return fmt.Errorf("create %s: %w", logical, err)
		}
	}
	return nil
}
