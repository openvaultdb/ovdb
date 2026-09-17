package demo

import (
	"encoding/json"
	"testing"
	"time"
)

// TestSeedGoldenBatch pins the exact batch of ops the demo's seed sends to
// the data API at the fixed clock demo_test.go's fixture uses. It is
// captured before todo-demo#REQ:seed-from-shared-package switches seed to
// the shared github.com/ingitdb/ingitdb-go/ingitdb/demos/todo package, and
// must keep passing unmodified afterwards: same paths, same JSON-encoded
// data bytes, in the same order (AC:seed-from-shared-package).
func TestSeedGoldenBatch(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)
	s := &Service{Now: func() time.Time { return now }}
	ops := s.seed()
	got, err := json.Marshal(ops)
	if err != nil {
		t.Fatal(err)
	}
	const golden = `[{"key":"/lists/to-buy","data":{"title":"To buy"}},{"key":"/lists/to-buy/items/milk","data":{"added_at":"2026-09-17T09:59:56Z","done":false,"title":"Milk"}},{"key":"/lists/to-buy/items/bananas","data":{"added_at":"2026-09-17T09:59:57Z","done":false,"title":"Bananas"}},{"key":"/lists/to-buy/items/coffee","data":{"added_at":"2026-09-17T09:59:58Z","done":false,"title":"Coffee"}},{"key":"/lists/to-watch","data":{"title":"To watch"}},{"key":"/lists/to-watch/items/the-matrix","data":{"added_at":"2026-09-17T09:59:59Z","done":false,"title":"The Matrix"}},{"key":"/lists/to-watch/items/interstellar","data":{"added_at":"2026-09-17T10:00:00Z","done":false,"title":"Interstellar"}}]`
	if string(got) != golden {
		t.Errorf("seed batch changed:\n got  = %s\n want = %s", got, golden)
	}
}
