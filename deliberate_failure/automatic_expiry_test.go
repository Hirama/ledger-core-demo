//go:build designgap

package deliberate_failure_test

import (
	"testing"

	ledger "github.com/Hirama/ledger-core-demo"
)

// Run explicitly: go test -tags designgap ./deliberate_failure
// This test must fail; the normal suite omits this file via its build tag.
func TestUnsettledAuthorizationAutomaticallyExpires(t *testing.T) {
	l := ledger.NewDemo()
	if err := l.Post(ledger.Event{ID: "fund", BookedDay: 1, Kind: "CREDIT", AccountID: "ACC-001", Currency: "AED", ValueDay: 1, Amount: "100.00"}); err != nil {
		t.Fatal(err)
	}
	if err := l.Post(ledger.Event{ID: "hold", BookedDay: 1, Kind: "AUTHORIZATION", AccountID: "ACC-001", Currency: "AED", ValueDay: 1, Amount: "60.00", AuthID: "stale-hold"}); err != nil {
		t.Fatal(err)
	}
	if err := l.Finish(); err != nil {
		t.Fatal(err)
	}
	// INTENTIONALLY FAILING: only an explicit EXPIRE command releases a stale
	// authorization. Closing Day 6 leaves this one ACTIVE; no automatic timeout
	// or expiry scheduler exists. Forgotten holds therefore reserve funds
	// indefinitely. This assertion exposes a production gap in our own design.
	if got := l.Authorizations("ACC-001")["stale-hold"].State; got != "EXPIRED" {
		t.Fatalf("got %s; want EXPIRED: no automatic expiry exists; explicit EXPIRE is required", got)
	}
}
