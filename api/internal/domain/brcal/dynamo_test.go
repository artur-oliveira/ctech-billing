package brcal

import (
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

func TestDateMarshalsAsAnISOString(t *testing.T) {
	// If this ever regresses to a nested map, every stored date silently changes
	// shape and every previously written row stops decoding.
	av, err := attributevalue.Marshal(New(2026, time.March, 10))
	if err != nil {
		t.Fatal(err)
	}
	s, ok := av.(*types.AttributeValueMemberS)
	if !ok {
		t.Fatalf("Date marshalled as %T, want a string attribute", av)
	}
	if s.Value != "2026-03-10" {
		t.Fatalf("got %q", s.Value)
	}
}

func TestDateRoundTripsThroughDynamoDB(t *testing.T) {
	type row struct {
		Due   Date `dynamodbav:"due_date"`
		Unset Date `dynamodbav:"unset"`
	}
	in := row{Due: New(2026, time.December, 31)}

	item, err := attributevalue.MarshalMap(in)
	if err != nil {
		t.Fatal(err)
	}
	if _, isNull := item["unset"].(*types.AttributeValueMemberNULL); !isNull {
		t.Fatalf("an unset date must marshal as NULL, got %T", item["unset"])
	}

	var out row
	if err := attributevalue.UnmarshalMap(item, &out); err != nil {
		t.Fatal(err)
	}
	if out.Due != in.Due {
		t.Fatalf("round trip: got %s, want %s", out.Due, in.Due)
	}
	if !out.Unset.IsZero() {
		t.Fatalf("an unset date came back as %s", out.Unset)
	}
}

func TestDateUnmarshalRejectsGarbage(t *testing.T) {
	var d Date
	if err := d.UnmarshalDynamoDBAttributeValue(&types.AttributeValueMemberS{Value: "10/03/2026"}); err == nil {
		t.Fatal("a non-ISO stored value must fail loudly, not decode to a wrong date")
	}
}
