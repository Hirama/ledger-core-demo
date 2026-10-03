package ledger

import (
	"bytes"
	"math"
	"reflect"
	"strings"
	"testing"
)

func equal(t *testing.T, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v; want %#v", got, want)
	}
}

func noError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func errorCode(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || err.Error() != want {
		t.Fatalf("got error %v; want %s", err, want)
	}
}

func recordsOf(l *Ledger, kind, account string) []Record {
	var out []Record
	for _, r := range l.Records() {
		if r.Kind == kind && (account == "" || r.AccountID == account) {
			out = append(out, r)
		}
	}
	return out
}

func prefix(t *testing.T, count int) *Ledger {
	t.Helper()
	l := NewDemo()
	for _, e := range DemoEvents()[:count] {
		err := l.Post(e)
		switch e.ID {
		case "E6":
			errorCode(t, err, "UNKNOWN_AUTHORIZATION")
		case "E8":
			errorCode(t, err, "INSUFFICIENT_AVAILABLE_BALANCE")
		default:
			noError(t, err)
		}
	}
	return l
}

func fullReplay(t *testing.T) *Ledger {
	t.Helper()
	l, err := Replay()
	noError(t, err)
	return l
}

func cashEvent(id string, day int, kind, amount string) Event {
	return Event{ID: id, BookedDay: day, Kind: kind, AccountID: "ACC-001", Currency: "AED", ValueDay: day, Amount: amount}
}

func authEvent(id, kind, amount, auth string) Event {
	e := cashEvent(id, 1, kind, amount)
	e.AuthID = auth
	return e
}

func funded(t *testing.T) *Ledger {
	t.Helper()
	l := NewDemo()
	noError(t, l.Post(cashEvent("fund", 1, "CREDIT", "100.00")))
	return l
}

func TestDayTwoPrincipalBeforeAnyFee(t *testing.T) {
	l := prefix(t, 7)
	var principal int64
	for _, r := range l.Records() {
		if r.AccountID == "ACC-001" && r.ValueDay <= 2 && (r.Kind == "CREDIT" || r.Kind == "DEBIT") {
			principal += r.AmountMinor
		}
	}
	equal(t, principal, int64(-37000))
	equal(t, l.Balance("ACC-001", 2), int64(-39500))
}

func TestBackdatedDebitAssessesThreeFees(t *testing.T) {
	l := prefix(t, 9) // entering Day 6 closes Day 5 before E9 reverses E7
	fees := recordsOf(l, "FEE", "ACC-001")
	equal(t, len(fees), 3)
	var total int64
	for i, day := range []int{2, 4, 5} {
		equal(t, fees[i].ValueDay, day)
		equal(t, fees[i].ObservedDay, 5)
		equal(t, fees[i].AmountMinor, int64(-2500))
		total += fees[i].AmountMinor
	}
	equal(t, total, int64(-7500))
}

func TestAuthASettlementAcceptedAndFullHoldReleased(t *testing.T) {
	l := prefix(t, 4)
	equal(t, l.Balance("ACC-001", 3), int64(65000))
	equal(t, l.ActiveHolds("ACC-001"), int64(20000))
	noError(t, l.Post(DemoEvents()[4]))
	a := l.Authorizations("ACC-001")["Auth-A"]
	equal(t, a.State, "SETTLED")
	equal(t, a.HoldMinor, int64(20000))
	equal(t, l.Balance("ACC-001", 4), int64(46500))
	equal(t, l.ActiveHolds("ACC-001"), int64(0))
	available, err := l.Available("ACC-001", 4)
	noError(t, err)
	equal(t, available, int64(46500))
}

func TestUnknownSettlementRejectedWithoutDebit(t *testing.T) {
	l := prefix(t, 5)
	before := l.Balance("ACC-001", 4)
	errorCode(t, l.Post(DemoEvents()[5]), "UNKNOWN_AUTHORIZATION")
	equal(t, l.Balance("ACC-001", 4), before)
	for _, r := range recordsOf(l, "SETTLEMENT", "") {
		if r.SourceID == "E6" {
			t.Fatal("unknown authorization produced settlement cash")
		}
	}
	equal(t, recordsOf(l, "ERROR", "")[0].Error, "UNKNOWN_AUTHORIZATION")
}

func TestAuthBDeclinedAndDecisionSurvivesReversal(t *testing.T) {
	l := prefix(t, 7)
	before := l.Balance("ACC-001", 5)
	errorCode(t, l.Post(DemoEvents()[7]), "INSUFFICIENT_AVAILABLE_BALANCE")
	equal(t, l.Authorizations("ACC-001")["Auth-B"].State, "DECLINED")
	equal(t, l.ActiveHolds("ACC-001"), int64(0))
	equal(t, l.Balance("ACC-001", 5), before)
	noError(t, l.Post(DemoEvents()[8]))
	equal(t, l.Authorizations("ACC-001")["Auth-B"].State, "DECLINED")
}

func TestApprovedAuthBReducesAvailableOnly(t *testing.T) {
	// The supplied Auth-B is declined. This funded variant exercises the
	// acceptance criterion's approved branch instead of passing vacuously.
	l := funded(t)
	noError(t, l.Post(authEvent("hold", "AUTHORIZATION", "90.00", "Auth-B")))
	equal(t, l.Balance("ACC-001", 1), int64(10000))
	equal(t, l.ActiveHolds("ACC-001"), int64(9000))
	available, err := l.Available("ACC-001", 1)
	noError(t, err)
	equal(t, available, int64(1000))
}

func TestReversalRetainsFeesAndOriginalRecords(t *testing.T) {
	l := prefix(t, 8)
	before := l.Records()
	noError(t, l.Post(DemoEvents()[8]))
	equal(t, l.Records()[:len(before)], before)
	equal(t, l.Balance("ACC-001", 5), int64(39000))
	var fees int64
	for _, r := range recordsOf(l, "FEE", "") {
		fees += r.AmountMinor
	}
	equal(t, fees, int64(-7500))
	r := recordsOf(l, "REVERSAL", "")[0]
	equal(t, r.AmountMinor, int64(62000))
	equal(t, r.ValueDay, 2)
	equal(t, r.RefID, "E7")
	equal(t, l.Authorizations("ACC-001")["Auth-A"].State, "SETTLED")
	equal(t, l.Authorizations("ACC-001")["Auth-B"].State, "DECLINED")
}

func TestBHDInstallmentsConserveCredit(t *testing.T) {
	l := fullReplay(t)
	parts := recordsOf(l, "INSTALLMENT", "ACC-002")
	equal(t, len(parts), 3)
	var total int64
	for i, amount := range []int64{3334, 3333, 3333} {
		equal(t, parts[i].AmountMinor, amount)
		equal(t, parts[i].Part, i+1)
		equal(t, parts[i].PartCount, 3)
		total += parts[i].AmountMinor
	}
	equal(t, total, int64(10000))
	equal(t, l.Balance("ACC-002", 5), int64(10000))
}

func TestRoundedAccrualsEqualCapitalization(t *testing.T) {
	l := fullReplay(t)
	for _, tc := range []struct {
		account string
		daily   []int64
		total   int64
	}{{"ACC-001", []int64{10, 9, 25, 17, 16, 16}, 93}, {"ACC-002", []int64{0, 0, 0, 0, 4, 4}, 8}} {
		t.Run(tc.account, func(t *testing.T) {
			var total int64
			for i, amount := range tc.daily {
				equal(t, l.Accrued(tc.account, i+1), amount)
				total += l.Accrued(tc.account, i+1)
			}
			equal(t, total, tc.total)
			caps := recordsOf(l, "INTEREST_CAPITALIZATION", tc.account)
			equal(t, len(caps), 1)
			equal(t, caps[0].ValueDay, 6)
			equal(t, caps[0].AmountMinor, total)
		})
	}
}

func TestFinalHistoricalBalances(t *testing.T) {
	l := fullReplay(t)
	for i, balance := range []int64{25000, 22500, 62500, 41500, 39000, 39000} {
		equal(t, l.BalanceBeforeCapitalization("ACC-001", i+1), balance)
	}
	equal(t, l.Balance("ACC-001", 6), int64(39093))
	equal(t, l.Balance("ACC-002", 6), int64(10008))
	equal(t, l.Accrued("ACC-001", 6), int64(16))
}

func TestExactInputOrderAndLateBooking(t *testing.T) {
	l := fullReplay(t)
	receipts := recordsOf(l, "RECEIVED", "")
	equal(t, len(receipts), 10)
	observed := []int{1, 1, 2, 3, 4, 4, 5, 5, 6, 6}
	for i, e := range DemoEvents() {
		equal(t, receipts[i].SourceID, e.ID)
		equal(t, receipts[i].ObservedDay, observed[i])
		equal(t, receipts[i].Event, e)
	}
	equal(t, receipts[9].BookedDay, 5)
	for _, r := range recordsOf(l, "INSTALLMENT", "") {
		equal(t, r.BookedDay, 5)
		equal(t, r.ObservedDay, 6)
		equal(t, r.ValueDay, 5)
	}
}

func TestReplayErrorsAndDailyOutput(t *testing.T) {
	l := fullReplay(t)
	errs := recordsOf(l, "ERROR", "")
	equal(t, len(errs), 2)
	equal(t, errs[0].SourceID, "E6")
	equal(t, errs[0].Error, "UNKNOWN_AUTHORIZATION")
	equal(t, errs[1].SourceID, "E8")
	equal(t, errs[1].Error, "INSUFFICIENT_AVAILABLE_BALANCE")
	var out bytes.Buffer
	PrintReport(&out, l)
	for _, text := range []string{"Day 1", "Day 2", "Day 3", "Day 4", "Day 5", "Day 6", "390.93", "10.008", "UNKNOWN_AUTHORIZATION", "Auth-A", "Auth-B"} {
		if !strings.Contains(out.String(), text) {
			t.Fatalf("daily report missing %q", text)
		}
	}
}

func TestInterestRestatedByAppendingAdjustments(t *testing.T) {
	l := prefix(t, 6)
	equal(t, l.Accrued("ACC-001", 2), int64(10))
	equal(t, l.Accrued("ACC-001", 3), int64(26))
	before := l.Records()
	cutoff := before[len(before)-1].Sequence
	noError(t, l.Post(DemoEvents()[6]))
	equal(t, l.Records()[:len(before)], before)
	for day := 2; day <= 4; day++ {
		equal(t, l.Accrued("ACC-001", day), int64(0))
	}
	equal(t, l.AccruedAt("ACC-001", 2, cutoff), int64(10))
	equal(t, l.BalanceAt("ACC-001", 2, cutoff), int64(25000))
	negative := false
	for _, r := range recordsOf(l, "INTEREST_ADJUSTMENT", "") {
		negative = negative || r.AmountMinor < 0
	}
	if !negative {
		t.Fatal("restatement did not append a negative interest adjustment")
	}
	_ = l.Post(DemoEvents()[7])
	noError(t, l.Post(DemoEvents()[8]))
	for i, amount := range []int64{10, 9, 25, 17, 16} {
		equal(t, l.Accrued("ACC-001", i+1), amount)
	}
}

func TestSnapshotsCannotMutateJournalOrConfiguration(t *testing.T) {
	accounts := []Account{{ID: "A", Currency: "AED", FeeMinor: 2500}}
	l, err := New(accounts)
	noError(t, err)
	accounts[0].OpeningMinor = 99999
	e := Event{ID: "credit", BookedDay: 1, Kind: "CREDIT", AccountID: "A", Currency: "AED", ValueDay: 1, Amount: "100.00"}
	noError(t, l.Post(e))
	e.Amount = "999.00"
	before := l.Records()
	snapshot := l.Records()
	snapshot[0].Kind = "CORRUPTED"
	snapshot[0].Event.Amount = "999.00"
	snapshot[1].AmountMinor = 99999
	configs := l.Accounts()
	configs[0].OpeningMinor = 99999
	equal(t, l.Records(), before)
	equal(t, l.Balance("A", 1), int64(10000))
	equal(t, l.Records()[0].Event.Amount, "100.00")
	for i, r := range l.Records() {
		equal(t, r.Sequence, i+1)
	}
}

func TestOriginalCloseAndApprovalSurviveRestatement(t *testing.T) {
	l := prefix(t, 4)
	var close Record
	for _, r := range recordsOf(l, "DAY_CLOSE", "ACC-001") {
		if r.ValueDay == 2 {
			close = r
		}
	}
	original := l.AuthorizationsAt("ACC-001", close.Sequence)["Auth-A"]
	equal(t, original.State, "ACTIVE")
	available, err := l.AvailableAt("ACC-001", 2, close.Sequence)
	noError(t, err)
	equal(t, available, int64(5000))
	for _, e := range DemoEvents()[4:8] {
		_ = l.Post(e)
	}
	equal(t, l.Balance("ACC-001", 2), int64(-39500))
	equal(t, l.BalanceAt("ACC-001", 2, close.Sequence), int64(25000))
	equal(t, l.AuthorizationsAt("ACC-001", close.Sequence)["Auth-A"], original)
	equal(t, len(recordsOf(l, "AUTH_APPROVED", "ACC-001")), 1)
}

func TestFinishAndFeeAssessmentAreIdempotent(t *testing.T) {
	l := prefix(t, 7)
	for _, id := range []string{"tail-a", "tail-b", "tail-c"} {
		noError(t, l.Post(cashEvent(id, 5, "CREDIT", "0.01")))
	}
	noError(t, l.Finish())
	before := l.Records()
	noError(t, l.Finish())
	equal(t, l.Records(), before)
	feeDays := map[int]bool{}
	for _, r := range recordsOf(l, "FEE", "ACC-001") {
		if feeDays[r.ValueDay] {
			t.Fatal("fee assessed twice on one day")
		}
		feeDays[r.ValueDay] = true
	}
	equal(t, feeDays, map[int]bool{2: true, 4: true, 5: true, 6: true})
	closes := recordsOf(l, "DAY_CLOSE", "")
	equal(t, len(closes), 12)
	keys := map[string]map[int]bool{}
	for _, r := range closes {
		if keys[r.AccountID] == nil {
			keys[r.AccountID] = map[int]bool{}
		}
		if keys[r.AccountID][r.ValueDay] {
			t.Fatal("duplicate day close")
		}
		keys[r.AccountID][r.ValueDay] = true
	}
}

func TestEmptyWindowAndPostFinalization(t *testing.T) {
	l := NewDemo()
	noError(t, l.Finish())
	equal(t, len(recordsOf(l, "DAY_CLOSE", "")), 12)
	equal(t, len(recordsOf(l, "INTEREST_CAPITALIZATION", "")), 2)
	equal(t, len(recordsOf(l, "FEE", "")), 0)
	errorCode(t, l.Post(cashEvent("late", 6, "CREDIT", "100.00")), "WINDOW_FINALIZED")
	equal(t, l.Balance("ACC-001", 6), int64(0))
	equal(t, len(recordsOf(l, "INTEREST_CAPITALIZATION", "")), 2)
	equal(t, recordsOf(l, "RECEIVED", "")[0].SourceID, "late")
}

func TestNegativeBHDReportsMissingFeeWithoutInventingFX(t *testing.T) {
	l := NewDemo()
	noError(t, l.Post(Event{ID: "bhd-debit", BookedDay: 1, Kind: "DEBIT", AccountID: "ACC-002", Currency: "BHD", ValueDay: 1, Amount: "1.000"}))
	noError(t, l.Post(Event{ID: "bhd-small", BookedDay: 2, Kind: "CREDIT", AccountID: "ACC-002", Currency: "BHD", ValueDay: 2, Amount: "0.001"}))
	noError(t, l.Finish())
	errs := recordsOf(l, "FEE_POLICY_ERROR", "ACC-002")
	equal(t, len(errs), 6)
	for i, r := range errs {
		equal(t, r.ValueDay, i+1)
		equal(t, r.Error, "OVERDRAFT_FEE_NOT_CONFIGURED")
	}
	equal(t, len(recordsOf(l, "FEE", "ACC-002")), 0)
	equal(t, l.Balance("ACC-002", 6), int64(-999))
	equal(t, l.Accrued("ACC-002", 0), int64(0))
}

func TestExactAvailableBoundaryAndAuthorizationSnapshot(t *testing.T) {
	l := funded(t)
	noError(t, l.Post(authEvent("a", "AUTHORIZATION", "60.00", "A")))
	noError(t, l.Post(authEvent("b", "AUTHORIZATION", "40.00", "B")))
	available, err := l.Available("ACC-001", 1)
	noError(t, err)
	equal(t, available, int64(0))
	errorCode(t, l.Post(authEvent("c", "AUTHORIZATION", "0.01", "C")), "INSUFFICIENT_AVAILABLE_BALANCE")
	equal(t, l.ActiveHolds("ACC-001"), int64(10000))
	snapshot := l.Authorizations("ACC-001")
	delete(snapshot, "A")
	snapshot["B"] = Authorization{State: "EXPIRED"}
	equal(t, l.ActiveHolds("ACC-001"), int64(10000))
	equal(t, l.Authorizations("ACC-001")["C"].State, "DECLINED")
}

func TestCancelAndExpireReleaseWithoutCash(t *testing.T) {
	for _, tc := range []struct{ kind, state string }{{"CANCEL", "CANCELLED"}, {"EXPIRE", "EXPIRED"}} {
		t.Run(tc.kind, func(t *testing.T) {
			l := funded(t)
			noError(t, l.Post(authEvent("auth", "AUTHORIZATION", "60.00", "A")))
			noError(t, l.Post(authEvent("release", tc.kind, "", "A")))
			equal(t, l.Authorizations("ACC-001")["A"].State, tc.state)
			equal(t, l.ActiveHolds("ACC-001"), int64(0))
			equal(t, l.Balance("ACC-001", 1), int64(10000))
			errorCode(t, l.Post(authEvent("settle", "SETTLEMENT", "60.00", "A")), "AUTHORIZATION_NOT_ACTIVE")
		})
	}
}

func TestDeclinedAuthorizationCannotEndOrBeReused(t *testing.T) {
	l := funded(t)
	errorCode(t, l.Post(authEvent("auth", "AUTHORIZATION", "100.01", "A")), "INSUFFICIENT_AVAILABLE_BALANCE")
	for _, kind := range []string{"SETTLEMENT", "CANCEL", "EXPIRE"} {
		amount := ""
		if kind == "SETTLEMENT" {
			amount = "1.00"
		}
		errorCode(t, l.Post(authEvent(kind, kind, amount, "A")), "AUTHORIZATION_NOT_ACTIVE")
	}
	errorCode(t, l.Post(authEvent("reuse", "AUTHORIZATION", "1.00", "A")), "DUPLICATE_AUTH_ID")
	equal(t, l.Balance("ACC-001", 1), int64(10000))
}

func TestAuthorizationIDReservedAfterTerminalState(t *testing.T) {
	for _, terminal := range []string{"CANCEL", "EXPIRE", "SETTLEMENT"} {
		t.Run(terminal, func(t *testing.T) {
			l := funded(t)
			noError(t, l.Post(authEvent("auth", "AUTHORIZATION", "50.00", "A")))
			amount := ""
			if terminal == "SETTLEMENT" {
				amount = "40.00"
			}
			noError(t, l.Post(authEvent("end", terminal, amount, "A")))
			errorCode(t, l.Post(authEvent("reuse", "AUTHORIZATION", "1.00", "A")), "DUPLICATE_AUTH_ID")
		})
	}
}

func TestAuthorizationIDsScopedToAccounts(t *testing.T) {
	l := funded(t)
	noError(t, l.Post(Event{ID: "bhd-fund", BookedDay: 1, Kind: "CREDIT", AccountID: "ACC-002", Currency: "BHD", ValueDay: 1, Amount: "1.000"}))
	noError(t, l.Post(authEvent("a", "AUTHORIZATION", "50.00", "A")))
	noError(t, l.Post(Event{ID: "b", BookedDay: 1, Kind: "AUTHORIZATION", AccountID: "ACC-002", Currency: "BHD", ValueDay: 1, Amount: "0.500", AuthID: "A"}))
	equal(t, l.ActiveHolds("ACC-001"), int64(5000))
	equal(t, l.ActiveHolds("ACC-002"), int64(500))
}

func TestDuplicateSettlementDoesNotDebitTwice(t *testing.T) {
	l := funded(t)
	noError(t, l.Post(authEvent("auth", "AUTHORIZATION", "60.00", "A")))
	noError(t, l.Post(authEvent("settle", "SETTLEMENT", "50.00", "A")))
	errorCode(t, l.Post(authEvent("settle-again", "SETTLEMENT", "50.00", "A")), "AUTHORIZATION_NOT_ACTIVE")
	equal(t, l.Balance("ACC-001", 1), int64(5000))
	equal(t, len(recordsOf(l, "SETTLEMENT", "")), 1)
}

func TestSettlementAboveHoldPreservesAuthorization(t *testing.T) {
	l := funded(t)
	noError(t, l.Post(authEvent("auth", "AUTHORIZATION", "60.00", "A")))
	errorCode(t, l.Post(authEvent("oversettle", "SETTLEMENT", "60.01", "A")), "SETTLEMENT_EXCEEDS_HOLD")
	equal(t, l.Balance("ACC-001", 1), int64(10000))
	equal(t, l.Authorizations("ACC-001")["A"].State, "ACTIVE")
	equal(t, l.ActiveHolds("ACC-001"), int64(6000))
}

func TestPreviouslyApprovedSettlementHonoredAfterOverdraft(t *testing.T) {
	l := funded(t)
	noError(t, l.Post(authEvent("auth", "AUTHORIZATION", "60.00", "A")))
	noError(t, l.Post(cashEvent("debit", 1, "DEBIT", "150.00")))
	noError(t, l.Post(authEvent("settle", "SETTLEMENT", "50.00", "A")))
	equal(t, l.Balance("ACC-001", 1), int64(-10000))
	equal(t, l.ActiveHolds("ACC-001"), int64(0))
}

func TestMalformedDatesCannotAdvanceClockOrAssessFees(t *testing.T) {
	l := NewDemo()
	noError(t, l.Post(cashEvent("overdraw", 1, "DEBIT", "1.00")))
	before := l.Records()
	bad := cashEvent("bad-clock", 6, "CREDIT", "10.00")
	bad.ValueDay = 7
	errorCode(t, l.Post(bad), "INVALID_DATES")
	equal(t, l.Records()[:len(before)], before)
	appended := l.Records()[len(before):]
	equal(t, len(appended), 2)
	equal(t, appended[0].Kind, "RECEIVED")
	equal(t, appended[1].Kind, "ERROR")
	equal(t, appended[0].ObservedDay, 1)
	equal(t, appended[1].ObservedDay, 1)
	equal(t, len(recordsOf(l, "DAY_CLOSE", "")), 0)
	equal(t, len(recordsOf(l, "FEE", "")), 0)
	equal(t, l.Balance("ACC-001", 1), int64(-100))
}

func TestInvalidCommandsAuditedWithoutCash(t *testing.T) {
	base := cashEvent("bad", 1, "CREDIT", "1.00")
	cases := []struct {
		name string
		edit func(*Event)
		code string
	}{
		{"account", func(e *Event) { e.AccountID = "absent" }, "UNKNOWN_ACCOUNT"},
		{"currency", func(e *Event) { e.Currency = "BHD" }, "CURRENCY_MISMATCH"},
		{"empty-id", func(e *Event) { e.ID = "" }, "EMPTY_EVENT_ID"},
		{"day-zero", func(e *Event) { e.BookedDay = 0 }, "INVALID_DATES"},
		{"day-seven", func(e *Event) { e.BookedDay = 7 }, "INVALID_DATES"},
		{"future-value", func(e *Event) { e.ValueDay = 2 }, "INVALID_DATES"},
		{"kind", func(e *Event) { e.Kind = "BOGUS" }, "UNKNOWN_EVENT_KIND"},
		{"nan", func(e *Event) { e.Amount = "NaN" }, "INVALID_AMOUNT"},
		{"zero-rounded", func(e *Event) { e.Amount = "0.004" }, "AMOUNT_MUST_ROUND_POSITIVE"},
		{"negative", func(e *Event) { e.Amount = "-1.00" }, "AMOUNT_MUST_ROUND_POSITIVE"},
		{"installments", func(e *Event) { e.Installments = -1 }, "INVALID_INSTALLMENTS"},
		{"debit-installments", func(e *Event) { e.Kind = "DEBIT"; e.Installments = 2 }, "INVALID_INSTALLMENTS"},
		{"missing-auth", func(e *Event) { e.Kind = "AUTHORIZATION" }, "AUTH_ID_REQUIRED"},
		{"backdated-auth", func(e *Event) { e.Kind = "AUTHORIZATION"; e.AuthID = "A"; e.BookedDay = 2 }, "BACKDATED_AUTH_LIFECYCLE_UNSUPPORTED"},
		{"reversal-amount", func(e *Event) { e.Kind = "REVERSAL"; e.RefID = "unknown" }, "REVERSAL_REQUIRES_REFERENCE_AND_NO_AMOUNT"},
		{"missing-ref", func(e *Event) { e.Kind = "REVERSAL"; e.Amount = "" }, "REVERSAL_REQUIRES_REFERENCE_AND_NO_AMOUNT"},
		{"release-amount", func(e *Event) { e.Kind = "CANCEL"; e.AuthID = "A" }, "RELEASE_REQUIRES_NO_AMOUNT"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := NewDemo()
			e := base
			tc.edit(&e)
			errorCode(t, l.Post(e), tc.code)
			equal(t, l.Balance("ACC-001", 6), int64(0))
			equal(t, len(recordsOf(l, "RECEIVED", "")), 1)
			equal(t, recordsOf(l, "ERROR", "")[0].Error, tc.code)
		})
	}
}

func TestDuplicateEventCannotDuplicateMoney(t *testing.T) {
	l := NewDemo()
	noError(t, l.Post(DemoEvents()[0]))
	errorCode(t, l.Post(DemoEvents()[0]), "DUPLICATE_EVENT_ID")
	equal(t, l.Balance("ACC-001", 1), int64(120000))
	equal(t, len(recordsOf(l, "CREDIT", "")), 1)
	equal(t, len(recordsOf(l, "RECEIVED", "")), 2)
}

func TestRejectedEventIDRemainsReserved(t *testing.T) {
	l := NewDemo()
	e := DemoEvents()[0]
	e.Amount = "bad"
	errorCode(t, l.Post(e), "INVALID_AMOUNT")
	errorCode(t, l.Post(DemoEvents()[0]), "DUPLICATE_EVENT_ID")
	equal(t, l.Balance("ACC-001", 1), int64(0))
}

func TestDuplicateReversalDoesNotRestoreTwice(t *testing.T) {
	l := prefix(t, 9)
	before := l.Balance("ACC-001", 6)
	e := DemoEvents()[8]
	e.ID = "E9-again"
	errorCode(t, l.Post(e), "ALREADY_REVERSED")
	equal(t, l.Balance("ACC-001", 6), before)
	equal(t, len(recordsOf(l, "REVERSAL", "")), 1)
}

func TestInvalidReversalReferences(t *testing.T) {
	for _, tc := range []struct {
		name, ref, account, currency string
		value                        int
		code                         string
	}{
		{"unknown", "absent-target", "ACC-001", "AED", 2, "UNKNOWN_REVERSAL_REFERENCE"},
		{"wrong-account", "E1", "ACC-002", "BHD", 1, "REVERSAL_ACCOUNT_MISMATCH"},
		{"auth", "E3", "ACC-001", "AED", 2, "UNSUPPORTED_REVERSAL_TARGET"},
		{"wrong-date", "E1", "ACC-001", "AED", 2, "REVERSAL_VALUE_DATE_MISMATCH"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := prefix(t, 6)
			before := l.Balance("ACC-001", 6)
			e := Event{ID: tc.name, BookedDay: 4, Kind: "REVERSAL", AccountID: tc.account, Currency: tc.currency, ValueDay: tc.value, RefID: tc.ref}
			errorCode(t, l.Post(e), tc.code)
			equal(t, l.Balance("ACC-001", 6), before)
		})
	}
}

func TestRejectedCashReceiptCannotBeReversed(t *testing.T) {
	l := NewDemo()
	errorCode(t, l.Post(cashEvent("bad", 1, "CREDIT", "bad")), "INVALID_AMOUNT")
	e := cashEvent("undo", 1, "REVERSAL", "")
	e.RefID = "bad"
	errorCode(t, l.Post(e), "REVERSAL_TARGET_NOT_POSTED")
	equal(t, l.Balance("ACC-001", 1), int64(0))
}

func TestInstallmentReversalRestoresExactAggregate(t *testing.T) {
	l := NewDemo()
	noError(t, l.Post(DemoEvents()[9]))
	noError(t, l.Post(Event{ID: "undo", BookedDay: 5, Kind: "REVERSAL", AccountID: "ACC-002", Currency: "BHD", ValueDay: 5, RefID: "E10"}))
	equal(t, l.Balance("ACC-002", 5), int64(0))
	equal(t, recordsOf(l, "REVERSAL", "")[0].AmountMinor, int64(-10000))
	equal(t, len(recordsOf(l, "INSTALLMENT", "")), 3)
}

func TestInvalidAccountConfiguration(t *testing.T) {
	for _, accounts := range [][]Account{nil, {{ID: "A", Currency: "USD"}}, {{ID: "A", Currency: "AED"}, {ID: "A", Currency: "AED"}}, {{ID: "A", Currency: "AED", FeeMinor: -1}}} {
		if _, err := New(accounts); err == nil {
			t.Fatalf("accepted invalid accounts %#v", accounts)
		}
	}
}

func TestMoneyPrecisionAndTiesToEven(t *testing.T) {
	for _, tc := range []struct {
		amount, currency string
		want             int64
	}{
		{"1.005", "AED", 100}, {"1.015", "AED", 102}, {"-1.015", "AED", -102},
		{"1.0005", "BHD", 1000}, {"1.0015", "BHD", 1002},
		{"010", "AED", 1000}, {"0009", "AED", 900},
	} {
		got, err := ParseMinor(tc.amount, tc.currency)
		noError(t, err)
		equal(t, got, tc.want)
	}
}

func TestMoneyRejectsMalformedAndOverflow(t *testing.T) {
	for _, amount := range []string{"NaN", "Infinity", "bad", "1e3", "1/2", "92233720368547758.08", "-92233720368547758.09"} {
		if _, err := ParseMinor(amount, "AED"); err == nil {
			t.Fatalf("accepted invalid amount %q", amount)
		}
	}
	for _, tc := range []struct {
		amount string
		want   int64
	}{{"92233720368547758.07", math.MaxInt64}, {"-92233720368547758.08", math.MinInt64}} {
		got, err := ParseMinor(tc.amount, "AED")
		noError(t, err)
		equal(t, got, tc.want)
		equal(t, FormatMoney(got, "AED"), tc.amount)
	}
}

func TestCashBalanceOverflowRejectedWithoutPosting(t *testing.T) {
	for _, tc := range []struct {
		name, kind string
		opening    int64
	}{{"upper", "CREDIT", math.MaxInt64}, {"lower", "DEBIT", math.MinInt64}} {
		t.Run(tc.name, func(t *testing.T) {
			l, err := New([]Account{{ID: "A", Currency: "AED", OpeningMinor: tc.opening}})
			noError(t, err)
			e := Event{ID: "overflow", BookedDay: 1, Kind: tc.kind, AccountID: "A", Currency: "AED", ValueDay: 1, Amount: "0.01"}
			errorCode(t, l.Post(e), "BALANCE_OVERFLOW")
			equal(t, l.Balance("A", 1), tc.opening)
			equal(t, len(l.Records()), 2)
			equal(t, l.Records()[0].Kind, "RECEIVED")
			equal(t, l.Records()[1].Kind, "ERROR")
		})
	}
}

func TestBackdatedCashChecksOverflowAtEveryAffectedDay(t *testing.T) {
	l := NewDemo()
	noError(t, l.Post(cashEvent("max", 2, "CREDIT", "92233720368547758.07")))
	before := l.Records()
	e := cashEvent("late", 2, "CREDIT", "0.01")
	e.ValueDay = 1
	// Day 1 has room, but the same posting would overflow Day 2. The whole
	// posting must be rejected rather than validating only its own value date.
	errorCode(t, l.Post(e), "BALANCE_OVERFLOW")
	equal(t, l.Records()[:len(before)], before)
	equal(t, l.Balance("ACC-001", 1), int64(0))
	equal(t, l.Balance("ACC-001", 2), int64(math.MaxInt64))
	equal(t, len(l.Records())-len(before), 2)
}

func TestCapitalizationOverflowHasNoPartialPayoutAndCanRetry(t *testing.T) {
	l, err := New([]Account{{ID: "A", Currency: "AED", OpeningMinor: 10000},
		{ID: "B", Currency: "AED", OpeningMinor: math.MaxInt64}})
	noError(t, err)
	errorCode(t, l.Finish(), "BALANCE_OVERFLOW")
	equal(t, len(recordsOf(l, "INTEREST_CAPITALIZATION", "")), 0)
	equal(t, len(recordsOf(l, "DAY_CLOSE", "")), 12)
	equal(t, l.Balance("A", 6), int64(10000))
	equal(t, l.Balance("B", 6), int64(math.MaxInt64))
	// Retrying an unresolved failure adds an error, but no duplicate closes or
	// partial credits. A failed finish must leave the window able to recover.
	errorCode(t, l.Finish(), "BALANCE_OVERFLOW")
	equal(t, len(recordsOf(l, "DAY_CLOSE", "")), 12)
	equal(t, len(recordsOf(l, "INTEREST_CAPITALIZATION", "")), 0)
	noError(t, l.Post(Event{ID: "make-room", BookedDay: 6, Kind: "DEBIT", AccountID: "B", Currency: "AED", ValueDay: 6, Amount: "92233720368547758.07"}))
	noError(t, l.Finish())
	equal(t, len(recordsOf(l, "INTEREST_CAPITALIZATION", "")), 2)
	for _, account := range []string{"A", "B"} {
		equal(t, len(recordsOf(l, "INTEREST_CAPITALIZATION", account)), 1)
		equal(t, recordsOf(l, "INTEREST_CAPITALIZATION", account)[0].AmountMinor, l.Accrued(account, 0))
	}
	before := l.Records()
	noError(t, l.Finish())
	equal(t, l.Records(), before)
}

func TestInstallmentSplitsConserveSmallTotals(t *testing.T) {
	parts, err := SplitMinor(10000, 3)
	noError(t, err)
	equal(t, parts, []int64{3334, 3333, 3333})
	for total := int64(0); total < 15; total++ {
		for count := 1; count < 8; count++ {
			parts, err := SplitMinor(total, count)
			noError(t, err)
			equal(t, len(parts), count)
			var sum int64
			for _, part := range parts {
				sum += part
				if part < 0 || part < parts[0]-1 || part > parts[0] {
					t.Fatalf("invalid distribution %#v", parts)
				}
			}
			equal(t, sum, total)
		}
	}
	for _, tc := range []struct {
		total int64
		count int
	}{{-1, 3}, {1, 0}, {1, -1}} {
		if _, err := SplitMinor(tc.total, tc.count); err == nil {
			t.Fatalf("accepted invalid split %#v", tc)
		}
	}
}
