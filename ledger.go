// Package ledger implements an append-only, single-writer in-memory subledger.
package ledger

import (
	"errors"
	"math/big"
)

type Account struct {
	ID, Currency           string
	OpeningMinor, FeeMinor int64 // FeeMinor zero means no configured fee policy.
}

type Event struct {
	ID                        string
	BookedDay                 int
	Kind, AccountID, Currency string
	ValueDay                  int
	Amount, AuthID, RefID     string
	Installments              int // Zero is the Go default: one posting.
}

// All fields are values, including the original Event. Snapshots have no mutable
// maps, slices or pointers that could alias authoritative journal state.
type Record struct {
	Sequence                                                       int
	Kind, SourceID, AccountID                                      string
	BookedDay, ObservedDay, ValueDay                               int
	AmountMinor                                                    int64
	AuthID, RefID, Error                                           string
	Event                                                          Event
	Part, PartCount                                                int
	AvailableBeforeMinor, TargetMinor, LedgerMinor, AvailableMinor int64
}

type Authorization struct {
	ID, State                     string
	HoldMinor                     int64
	DecisionSequence, EndSequence int
}

type Ledger struct {
	accounts    []Account
	configs     map[string]Account
	records     []Record
	seen        map[string]bool
	closed      map[int]bool
	observedDay int
	finalized   bool
}

func New(accounts []Account) (*Ledger, error) {
	if len(accounts) == 0 {
		return nil, errors.New("EMPTY_ACCOUNTS")
	}
	ledger := &Ledger{accounts: append([]Account(nil), accounts...), configs: make(map[string]Account),
		seen: make(map[string]bool), closed: make(map[int]bool), observedDay: 1}
	for _, account := range accounts {
		if account.ID == "" || ledger.configs[account.ID].ID != "" {
			return nil, errors.New("INVALID_ACCOUNT_ID")
		}
		if _, ok := precision(account.Currency); !ok {
			return nil, errors.New("UNSUPPORTED_CURRENCY")
		}
		if account.FeeMinor < 0 {
			return nil, errors.New("INVALID_FEE")
		}
		ledger.configs[account.ID] = account
	}
	return ledger, nil
}

func NewDemo() *Ledger {
	ledger, err := New([]Account{{ID: "ACC-001", Currency: "AED", FeeMinor: 2500},
		{ID: "ACC-002", Currency: "BHD"}})
	if err != nil {
		panic(err)
	}
	return ledger
}

func (l *Ledger) Accounts() []Account { return append([]Account(nil), l.accounts...) }
func (l *Ledger) Records() []Record   { return append([]Record(nil), l.records...) }
func (l *Ledger) append(record Record) {
	record.Sequence = len(l.records) + 1
	record.ObservedDay = l.observedDay
	l.records = append(l.records, record)
}
func (l *Ledger) generated(kind, account string, day int, amount int64) Record {
	return Record{Kind: kind, AccountID: account, BookedDay: l.observedDay, ValueDay: day, AmountMinor: amount}
}
func isCash(kind string) bool {
	switch kind {
	case "CREDIT", "DEBIT", "INSTALLMENT", "SETTLEMENT", "REVERSAL", "FEE", "INTEREST_CAPITALIZATION":
		return true
	}
	return false
}

func (l *Ledger) balance(account string, day, sequence int, capitalization bool) int64 {
	config, ok := l.configs[account]
	if !ok {
		panic("unknown account in balance query")
	}
	total := config.OpeningMinor
	for _, record := range l.records {
		if record.AccountID != account || !isCash(record.Kind) || record.ValueDay > day ||
			(sequence != 0 && record.Sequence > sequence) || (!capitalization && record.Kind == "INTEREST_CAPITALIZATION") {
			continue
		}
		var err error
		total, err = checkedAdd(total, record.AmountMinor)
		if err != nil {
			panic("journal invariant: overflowing balance")
		}
	}
	return total
}
func (l *Ledger) Balance(account string, day int) int64 { return l.balance(account, day, 0, true) }
func (l *Ledger) BalanceAt(account string, day, sequence int) int64 {
	return l.balance(account, day, sequence, true)
}
func (l *Ledger) BalanceBeforeCapitalization(account string, day int) int64 {
	return l.balance(account, day, 0, false)
}

func (l *Ledger) Authorizations(account string) map[string]Authorization {
	return l.AuthorizationsAt(account, 0)
}
func (l *Ledger) AuthorizationsAt(account string, sequence int) map[string]Authorization {
	states := make(map[string]Authorization)
	for _, record := range l.records {
		if record.AccountID != account || (sequence != 0 && record.Sequence > sequence) {
			continue
		}
		switch record.Kind {
		case "AUTH_APPROVED", "AUTH_DECLINED":
			state := "ACTIVE"
			if record.Kind == "AUTH_DECLINED" {
				state = "DECLINED"
			}
			states[record.AuthID] = Authorization{ID: record.AuthID, State: state, HoldMinor: record.AmountMinor, DecisionSequence: record.Sequence}
		case "SETTLEMENT", "CANCELLED", "EXPIRED":
			original := states[record.AuthID]
			original.State = record.Kind
			if record.Kind == "SETTLEMENT" {
				original.State = "SETTLED"
			}
			original.EndSequence = record.Sequence
			states[record.AuthID] = original
		}
	}
	return states
}
func (l *Ledger) ActiveHolds(account string) int64 { return l.ActiveHoldsAt(account, 0) }
func (l *Ledger) ActiveHoldsAt(account string, sequence int) int64 {
	var total int64
	for _, auth := range l.AuthorizationsAt(account, sequence) {
		if auth.State == "ACTIVE" {
			var err error
			total, err = checkedAdd(total, auth.HoldMinor)
			if err != nil {
				panic("hold overflow invariant")
			}
		}
	}
	return total
}
func (l *Ledger) Available(account string, day int) (int64, error) {
	return l.AvailableAt(account, day, 0)
}
func (l *Ledger) AvailableAt(account string, day, sequence int) (int64, error) {
	return checkedSubtract(l.BalanceAt(account, day, sequence), l.ActiveHoldsAt(account, sequence))
}
func (l *Ledger) Accrued(account string, day int) int64 { return l.AccruedAt(account, day, 0) }
func (l *Ledger) AccruedAt(account string, day, sequence int) int64 {
	var total int64
	for _, record := range l.records {
		if record.AccountID == account && (record.Kind == "INTEREST_ACCRUAL" || record.Kind == "INTEREST_ADJUSTMENT") &&
			(day == 0 || record.ValueDay == day) && (sequence == 0 || record.Sequence <= sequence) {
			var err error
			total, err = checkedAdd(total, record.AmountMinor)
			if err != nil {
				panic("interest overflow invariant")
			}
		}
	}
	return total
}

func validDates(event Event) bool {
	return 1 <= event.ValueDay && event.ValueDay <= event.BookedDay && event.BookedDay <= WindowDays
}
func (l *Ledger) validate(event Event) error {
	reject := func(code string) error { return errors.New(code) }
	if event.ID == "" {
		return reject("EMPTY_EVENT_ID")
	}
	account, ok := l.configs[event.AccountID]
	if !ok {
		return reject("UNKNOWN_ACCOUNT")
	}
	if event.Currency != account.Currency {
		return reject("CURRENCY_MISMATCH")
	}
	if !validDates(event) {
		return reject("INVALID_DATES")
	}
	switch event.Kind {
	case "CREDIT", "DEBIT", "AUTHORIZATION", "SETTLEMENT", "REVERSAL", "CANCEL", "EXPIRE":
	default:
		return reject("UNKNOWN_EVENT_KIND")
	}
	if event.Installments < 0 || (event.Installments > 1 && event.Kind != "CREDIT") {
		return reject("INVALID_INSTALLMENTS")
	}
	switch event.Kind {
	case "AUTHORIZATION", "SETTLEMENT", "CANCEL", "EXPIRE":
		if event.AuthID == "" {
			return reject("AUTH_ID_REQUIRED")
		}
		if event.ValueDay != event.BookedDay {
			return reject("BACKDATED_AUTH_LIFECYCLE_UNSUPPORTED")
		}
	}
	if event.Kind == "REVERSAL" && (event.RefID == "" || event.Amount != "") {
		return reject("REVERSAL_REQUIRES_REFERENCE_AND_NO_AMOUNT")
	}
	if (event.Kind == "CANCEL" || event.Kind == "EXPIRE") && event.Amount != "" {
		return reject("RELEASE_REQUIRES_NO_AMOUNT")
	}
	return nil
}

func (l *Ledger) reject(event Event, err error) error {
	l.append(Record{Kind: "ERROR", SourceID: event.ID, AccountID: event.AccountID,
		BookedDay: event.BookedDay, AuthID: event.AuthID, Error: err.Error()})
	return err
}

// Post serializes the supplied command with closes required by a later date.
// A receipt and any business rejection remain in the journal. Derived failures
// are returned explicitly; already-appended entries are never rolled back.
func (l *Ledger) Post(event Event) error {
	var advanceErr error
	if !l.finalized && validDates(event) && event.BookedDay > l.observedDay {
		for l.observedDay < event.BookedDay {
			if err := l.closeDay(l.observedDay); err != nil {
				advanceErr = err
				break
			}
			l.observedDay++
		}
	}
	l.append(Record{Kind: "RECEIVED", SourceID: event.ID, AccountID: event.AccountID, BookedDay: event.BookedDay, Event: event})
	if l.finalized {
		return l.reject(event, errors.New("WINDOW_FINALIZED"))
	}
	if l.seen[event.ID] {
		return l.reject(event, errors.New("DUPLICATE_EVENT_ID"))
	}
	l.seen[event.ID] = true
	if advanceErr != nil {
		return l.reject(event, advanceErr)
	}
	if err := l.validate(event); err != nil {
		return l.reject(event, err)
	}
	var amount int64
	switch event.Kind {
	case "CREDIT", "DEBIT", "AUTHORIZATION", "SETTLEMENT":
		var err error
		amount, err = ParseMinor(event.Amount, event.Currency)
		if err != nil {
			code := "INVALID_AMOUNT"
			if err.Error() == "AMOUNT_OVERFLOW" {
				code = "AMOUNT_OVERFLOW"
			}
			return l.reject(event, errors.New(code))
		}
		if amount <= 0 {
			return l.reject(event, errors.New("AMOUNT_MUST_ROUND_POSITIVE"))
		}
	}
	if err := l.apply(event, amount); err != nil {
		return l.reject(event, err)
	}
	return l.restateClosedDays()
}

func (l *Ledger) checkCash(account string, day int, amount int64) error {
	for target := day; target <= WindowDays; target++ {
		if _, err := checkedAdd(l.Balance(account, target), amount); err != nil {
			return err
		}
	}
	return nil
}
func (l *Ledger) addCash(record Record) error {
	if err := l.checkCash(record.AccountID, record.ValueDay, record.AmountMinor); err != nil {
		return err
	}
	l.append(record)
	return nil
}
func (l *Ledger) apply(event Event, amount int64) error {
	record := Record{Kind: event.Kind, SourceID: event.ID, AccountID: event.AccountID,
		BookedDay: event.BookedDay, ValueDay: event.ValueDay, AuthID: event.AuthID}
	switch event.Kind {
	case "CREDIT", "DEBIT":
		if event.Installments > 1 {
			if err := l.checkCash(event.AccountID, event.ValueDay, amount); err != nil {
				return err
			}
			parts, err := SplitMinor(amount, event.Installments)
			if err != nil {
				return err
			}
			for i, part := range parts {
				copy := record
				copy.Kind = "INSTALLMENT"
				copy.AmountMinor = part
				copy.Part = i + 1
				copy.PartCount = len(parts)
				l.append(copy)
			}
			return nil
		}
		record.AmountMinor = amount
		if event.Kind == "DEBIT" {
			record.AmountMinor = -amount
		}
		return l.addCash(record)
	case "AUTHORIZATION":
		if _, exists := l.Authorizations(event.AccountID)[event.AuthID]; exists {
			return errors.New("DUPLICATE_AUTH_ID")
		}
		before, err := l.Available(event.AccountID, l.observedDay)
		if err != nil {
			return err
		}
		record.AmountMinor, record.AvailableBeforeMinor = amount, before
		if before >= amount {
			record.Kind = "AUTH_APPROVED"
			l.append(record)
			return nil
		}
		record.Kind = "AUTH_DECLINED"
		l.append(record)
		return errors.New("INSUFFICIENT_AVAILABLE_BALANCE")
	case "SETTLEMENT", "CANCEL", "EXPIRE":
		auth, ok := l.Authorizations(event.AccountID)[event.AuthID]
		if !ok {
			return errors.New("UNKNOWN_AUTHORIZATION")
		}
		if auth.State != "ACTIVE" {
			return errors.New("AUTHORIZATION_NOT_ACTIVE")
		}
		if event.Kind == "SETTLEMENT" {
			if amount > auth.HoldMinor {
				return errors.New("SETTLEMENT_EXCEEDS_HOLD")
			}
			record.AmountMinor = -amount
			return l.addCash(record)
		}
		record.Kind = "CANCELLED"
		if event.Kind == "EXPIRE" {
			record.Kind = "EXPIRED"
		}
		l.append(record)
		return nil
	case "REVERSAL":
		var original Event
		found := false
		for _, prior := range l.records {
			if prior.Kind == "RECEIVED" && prior.SourceID == event.RefID {
				original, found = prior.Event, true
				break
			}
		}
		if !found {
			return errors.New("UNKNOWN_REVERSAL_REFERENCE")
		}
		if original.AccountID != event.AccountID || original.Currency != event.Currency {
			return errors.New("REVERSAL_ACCOUNT_MISMATCH")
		}
		if original.Kind != "CREDIT" && original.Kind != "DEBIT" {
			return errors.New("UNSUPPORTED_REVERSAL_TARGET")
		}
		if original.ValueDay != event.ValueDay {
			return errors.New("REVERSAL_VALUE_DATE_MISMATCH")
		}
		var total int64
		posted := false
		for _, prior := range l.records {
			if prior.Kind == "REVERSAL" && prior.RefID == event.RefID {
				return errors.New("ALREADY_REVERSED")
			}
			if prior.SourceID == event.RefID && (prior.Kind == "CREDIT" || prior.Kind == "DEBIT" || prior.Kind == "INSTALLMENT") {
				total += prior.AmountMinor
				posted = true
			}
		}
		if !posted {
			return errors.New("REVERSAL_TARGET_NOT_POSTED")
		}
		record.AmountMinor, record.RefID = -total, event.RefID
		return l.addCash(record)
	}
	panic("validated event kind not handled")
}

func (l *Ledger) assessFee(account string, day int) error {
	for _, prior := range l.records {
		if prior.AccountID == account && prior.ValueDay == day && (prior.Kind == "FEE" || prior.Kind == "FEE_POLICY_ERROR") {
			return nil
		}
	}
	if l.BalanceBeforeCapitalization(account, day) >= 0 {
		return nil
	}
	fee := l.configs[account].FeeMinor
	if fee == 0 {
		record := l.generated("FEE_POLICY_ERROR", account, day, 0)
		record.Error = "OVERDRAFT_FEE_NOT_CONFIGURED"
		l.append(record)
		return nil
	}
	if err := l.addCash(l.generated("FEE", account, day, -fee)); err != nil {
		record := l.generated("ERROR", account, day, 0)
		record.Error = err.Error()
		l.append(record)
		return err
	}
	return nil
}
func (l *Ledger) reconcileInterest(account string, day int) {
	balance := l.BalanceBeforeCapitalization(account, day)
	var target int64
	if balance > 0 {
		target = roundRational(new(big.Rat).SetFrac(big.NewInt(balance), big.NewInt(dailyRateDenominator))).Int64()
	}
	previous := l.Accrued(account, day)
	exists := false
	for _, prior := range l.records {
		if prior.AccountID == account && prior.ValueDay == day && prior.Kind == "INTEREST_ACCRUAL" {
			exists = true
			break
		}
	}
	if !exists || target != previous {
		kind := "INTEREST_ACCRUAL"
		if exists {
			kind = "INTEREST_ADJUSTMENT"
		}
		record := l.generated(kind, account, day, target-previous)
		record.TargetMinor = target
		l.append(record)
	}
}
func (l *Ledger) restateClosedDays() error {
	for day := 1; day <= WindowDays; day++ {
		if l.closed[day] {
			for _, account := range l.accounts {
				if err := l.assessFee(account.ID, day); err != nil {
					return err
				}
				l.reconcileInterest(account.ID, day)
			}
		}
	}
	return nil
}
func (l *Ledger) closeDay(day int) error {
	if l.closed[day] {
		return nil
	}
	// All account cash changes precede close snapshots, so a snapshot represents
	// the complete cross-account close, rather than an intermediate iteration.
	for _, account := range l.accounts {
		if err := l.assessFee(account.ID, day); err != nil {
			return err
		}
		l.reconcileInterest(account.ID, day)
	}
	closes := make([]Record, 0, len(l.accounts))
	for _, account := range l.accounts {
		available, err := l.Available(account.ID, day)
		if err != nil {
			record := l.generated("ERROR", account.ID, day, 0)
			record.Error = err.Error()
			l.append(record)
			return err
		}
		record := l.generated("DAY_CLOSE", account.ID, day, 0)
		record.LedgerMinor, record.AvailableMinor = l.Balance(account.ID, day), available
		closes = append(closes, record)
	}
	for _, record := range closes {
		l.append(record)
	}
	l.closed[day] = true
	return nil
}
func (l *Ledger) Finish() error {
	if l.finalized {
		return nil
	}
	for l.observedDay <= WindowDays {
		if err := l.closeDay(l.observedDay); err != nil {
			return err
		}
		if l.observedDay == WindowDays {
			break
		}
		l.observedDay++
	}
	// Check every capitalization before appending any, avoiding partial payout.
	for _, account := range l.accounts {
		if err := l.checkCash(account.ID, WindowDays, l.Accrued(account.ID, 0)); err != nil {
			record := l.generated("ERROR", account.ID, WindowDays, 0)
			record.Error = err.Error()
			l.append(record)
			return err
		}
	}
	for _, account := range l.accounts {
		l.append(l.generated("INTEREST_CAPITALIZATION", account.ID, WindowDays, l.Accrued(account.ID, 0)))
	}
	l.finalized = true
	return nil
}
