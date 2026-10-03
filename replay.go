package ledger

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

func DemoEvents() []Event {
	return []Event{
		{ID: "E1", BookedDay: 1, Kind: "CREDIT", AccountID: "ACC-001", Currency: "AED", ValueDay: 1, Amount: "1200.00"},
		{ID: "E2", BookedDay: 1, Kind: "DEBIT", AccountID: "ACC-001", Currency: "AED", ValueDay: 1, Amount: "950.00"},
		{ID: "E3", BookedDay: 2, Kind: "AUTHORIZATION", AccountID: "ACC-001", Currency: "AED", ValueDay: 2, Amount: "200.00", AuthID: "Auth-A"},
		{ID: "E4", BookedDay: 3, Kind: "CREDIT", AccountID: "ACC-001", Currency: "AED", ValueDay: 3, Amount: "400.00"},
		{ID: "E5", BookedDay: 4, Kind: "SETTLEMENT", AccountID: "ACC-001", Currency: "AED", ValueDay: 4, Amount: "185.00", AuthID: "Auth-A"},
		{ID: "E6", BookedDay: 4, Kind: "SETTLEMENT", AccountID: "ACC-001", Currency: "AED", ValueDay: 4, Amount: "180.00", AuthID: "Auth-Z"},
		{ID: "E7", BookedDay: 5, Kind: "DEBIT", AccountID: "ACC-001", Currency: "AED", ValueDay: 2, Amount: "620.00"},
		{ID: "E8", BookedDay: 5, Kind: "AUTHORIZATION", AccountID: "ACC-001", Currency: "AED", ValueDay: 5, Amount: "90.00", AuthID: "Auth-B"},
		{ID: "E9", BookedDay: 6, Kind: "REVERSAL", AccountID: "ACC-001", Currency: "AED", ValueDay: 2, RefID: "E7"},
		{ID: "E10", BookedDay: 5, Kind: "CREDIT", AccountID: "ACC-002", Currency: "BHD", ValueDay: 5, Amount: "10.000", Installments: 3},
	}
}

func Replay() (*Ledger, error) {
	ledger := NewDemo()
	for _, event := range DemoEvents() {
		err := ledger.Post(event)
		if err != nil && event.ID != "E6" && event.ID != "E8" {
			return ledger, err
		}
	}
	return ledger, ledger.Finish()
}

// PrintReport shows both original close knowledge and final restated balances.
func PrintReport(writer io.Writer, ledger *Ledger) {
	records := ledger.Records()
	var inputs []string
	for _, record := range records {
		if record.Kind == "RECEIVED" {
			inputs = append(inputs, record.SourceID)
		}
	}
	fmt.Fprintln(writer, "INPUT ORDER: "+strings.Join(inputs, " -> "))
	fmt.Fprintln(writer, "Original close = knowledge at that close; restated = final knowledge, before capitalization.")
	for day := 1; day <= WindowDays; day++ {
		fmt.Fprintf(writer, "\nDay %d\n", day)
		for _, account := range ledger.Accounts() {
			var close Record
			for _, record := range records {
				if record.Kind == "DAY_CLOSE" && record.AccountID == account.ID && record.ValueDay == day {
					close = record
					break
				}
			}
			money := func(amount int64) string { return FormatMoney(amount, account.Currency) }
			auths := ledger.AuthorizationsAt(account.ID, close.Sequence)
			names := make([]string, 0, len(auths))
			for name := range auths {
				names = append(names, name)
			}
			sort.Strings(names)
			states := make([]string, 0, len(names))
			for _, name := range names {
				states = append(states, name+"="+auths[name].State)
			}
			stateText := "none"
			if len(states) > 0 {
				stateText = strings.Join(states, ", ")
			}
			fmt.Fprintf(writer, "  %s %s: original close=%s; restated close=%s; rounded daily interest=%s\n",
				account.ID, account.Currency, money(ledger.BalanceAt(account.ID, day, close.Sequence)),
				money(ledger.BalanceBeforeCapitalization(account.ID, day)), money(ledger.Accrued(account.ID, day)))
			fmt.Fprintf(writer, "    authorizations at close: %s; active holds=%s; available at close=%s\n",
				stateText, money(ledger.ActiveHoldsAt(account.ID, close.Sequence)), money(close.AvailableMinor))
			var fees []string
			for _, record := range records {
				if record.Kind == "FEE" && record.AccountID == account.ID && record.ValueDay == day {
					fees = append(fees, fmt.Sprintf("%s (observed Day %d, seq %d)", money(-record.AmountMinor), record.ObservedDay, record.Sequence))
				}
			}
			feeText := "none"
			if len(fees) > 0 {
				feeText = strings.Join(fees, ", ")
			}
			fmt.Fprintln(writer, "    fee assessments for this day: "+feeText)
		}
		var observedErrors []string
		for _, record := range records {
			if record.Error != "" && record.ObservedDay == day {
				name := record.SourceID
				if name == "" {
					name = record.AccountID
				}
				observedErrors = append(observedErrors, name+": "+record.Error)
			}
		}
		errorText := "none"
		if len(observedErrors) > 0 {
			errorText = strings.Join(observedErrors, "; ")
		}
		fmt.Fprintln(writer, "  errors observed today: "+errorText)
	}
	fmt.Fprintln(writer, "\nEnd of Day 6 after capitalization")
	for _, account := range ledger.Accounts() {
		fmt.Fprintf(writer, "  %s %s: ledger=%s; capitalized=%s\n", account.ID, account.Currency,
			FormatMoney(ledger.Balance(account.ID, WindowDays), account.Currency), FormatMoney(ledger.Accrued(account.ID, 0), account.Currency))
	}
	fmt.Fprintln(writer, "\nAudit adjustments (append-only; original accruals remain)")
	for _, record := range records {
		if record.Kind == "INTEREST_ADJUSTMENT" {
			currency := ledger.configs[record.AccountID].Currency
			fmt.Fprintf(writer, "  seq %d: %s, value Day %d, observed Day %d, delta %s\n", record.Sequence, record.AccountID,
				record.ValueDay, record.ObservedDay, FormatMoney(record.AmountMinor, currency))
		}
	}
}
