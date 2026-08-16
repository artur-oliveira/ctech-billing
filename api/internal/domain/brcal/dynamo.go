package brcal

import (
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// Date stores and loads as the string "YYYY-MM-DD".
//
// The AWS attributevalue package does not honour encoding.TextMarshaler, so
// without these two methods a Date would persist as a nested map
// {"Year":2026,"Month":3,"Day":10}. That is worse in every way that matters:
// it cannot be range-compared in a key condition, it triples the stored size,
// and — the one that actually costs time — it is unreadable when a human is
// looking at a row during an incident.
//
// This is the only place the domain touches an AWS package, and it is a
// serialization interface rather than I/O: no client, no context, no network.
// The alternative was a parallel set of persistence structs mirroring every
// entity, and two definitions of the same shape drift the first time someone
// adds a field to one of them.

var (
	_ attributevalue.Marshaler   = Date{}
	_ attributevalue.Unmarshaler = (*Date)(nil)
)

// MarshalDynamoDBAttributeValue implements attributevalue.Marshaler.
func (d Date) MarshalDynamoDBAttributeValue() (types.AttributeValue, error) {
	if d.IsZero() {
		// An unset date is NULL rather than "0000-00-00", so `omitempty`-style
		// pruning can drop it instead of storing a date that never existed.
		return &types.AttributeValueMemberNULL{Value: true}, nil
	}
	return &types.AttributeValueMemberS{Value: d.String()}, nil
}

// UnmarshalDynamoDBAttributeValue implements attributevalue.Unmarshaler.
func (d *Date) UnmarshalDynamoDBAttributeValue(av types.AttributeValue) error {
	switch v := av.(type) {
	case *types.AttributeValueMemberNULL:
		*d = Date{}
		return nil
	case *types.AttributeValueMemberS:
		if v.Value == "" {
			*d = Date{}
			return nil
		}
		parsed, err := Parse(v.Value)
		if err != nil {
			return err
		}
		*d = parsed
		return nil
	default:
		*d = Date{}
		return nil
	}
}
