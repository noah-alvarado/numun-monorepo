package store

import (
	"context"
	"strconv"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// pagedScanner returns one page per call; matches mark which rows pass the filter.
type pagedScanner struct {
	pages  [][]bool
	calls  int
	limits []*int32
}

func (p *pagedScanner) Scan(_ context.Context, in *dynamodb.ScanInput, _ ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error) {
	p.limits = append(p.limits, in.Limit)
	page := p.pages[p.calls]
	p.calls++
	out := &dynamodb.ScanOutput{}
	for i, match := range page {
		if match {
			out.Items = append(out.Items, map[string]ddbtypes.AttributeValue{
				"id": &ddbtypes.AttributeValueMemberS{Value: strconv.Itoa(p.calls*100 + i)},
			})
		}
	}
	if p.calls < len(p.pages) {
		out.LastEvaluatedKey = map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: strconv.Itoa(p.calls)},
		}
	}
	return out, nil
}

func TestScanFirst_FollowsPagesPastEmptyFilteredPages(t *testing.T) {
	limit := int32(1)
	api := &pagedScanner{pages: [][]bool{{false}, {false, false}, {true}}}
	items, err := scanFirst(context.Background(), api, &dynamodb.ScanInput{Limit: &limit}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	if api.calls != 3 {
		t.Errorf("got %d Scan calls, want 3", api.calls)
	}
	for _, l := range api.limits {
		if l != nil {
			t.Errorf("Limit must be cleared, got %d", *l)
		}
	}
}

func TestScanFirst_StopsAtMax(t *testing.T) {
	api := &pagedScanner{pages: [][]bool{{true, true, true}, {true}}}
	items, err := scanFirst(context.Background(), api, &dynamodb.ScanInput{}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || api.calls != 1 {
		t.Errorf("got %d items over %d calls, want 2 over 1", len(items), api.calls)
	}
}

func TestScanFirst_NoMatch(t *testing.T) {
	api := &pagedScanner{pages: [][]bool{{false}, {false}}}
	items, err := scanFirst(context.Background(), api, &dynamodb.ScanInput{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 || api.calls != 2 {
		t.Errorf("got %d items over %d calls, want 0 over 2", len(items), api.calls)
	}
}
