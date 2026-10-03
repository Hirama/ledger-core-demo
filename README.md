# In memory account ledger

Go 1.22 or newer, standard library only. This core uses an append-only in-memory journal and derives balances and authorization states from it. No web layer, persistence, UI, or database.

## Run

From the repository root:

```sh
go test ./...
go run ./cmd/replay
```

The normal suite must pass. The replay prints both accounts for all six days: closing ledger balances, daily interest, fee assessments, authorization states, active holds, available balances, and errors. It also prints capitalization and appended interest adjustments. No packages or services need to be installed beyond Go. [examples/replay.txt](examples/replay.txt) is the captured console output.

The assignment also asks for one failing test against this design:

```sh
go test -tags designgap ./deliberate_failure
```

This command **must exit 1 with one failure**. The test is neither skipped nor treated as an expected failure. Its inline annotation explains that active holds do not expire automatically: a caller must supply `EXPIRE`. A build tag excludes it from normal correctness checks. Production would need a contractual deadline and a reliable expiry scheduler.

## Read the output

`original close` uses the journal sequence at the first close of that day. `restated close` includes later value-dated entries and retained fees, using final knowledge and excluding the end-of-window interest credit. Authorization states and available balances beside the original close use that same knowledge cutoff. Fees are listed against the day assessed, with the later day on which the assessment was observed. Errors are grouped by observed day. No original record is overwritten.

E10 stays after E9 in replay order. Its supplied booking/value day is 5, but its observation day is 6. Day 5 is restated before Day 6 is finalized. The final block includes the capitalization credit, which is excluded from its own interest base.

Expected final results:

| Account | Ledger before capitalization | Retained fees | Capitalized interest | Final ledger |
| --- | ---: | ---: | ---: | ---: |
| ACC-001 AED | 390.00 | 75.00 | 0.93 | 390.93 |
| ACC-002 BHD | 10.000 | 0.000 | 0.008 | 10.008 |

Auth-A is approved and settled; settlement releases its entire hold. Auth-Z is rejected without a debit. Auth-B is declined and creates no active hold. A funded variant in the suite verifies that an approved hold changes available balance while leaving ledger balance unchanged.

## Core API and audit

`ledger.go` holds the core, `money.go` the exact money operations, and `replay.go` the supplied stream/report. The executable lives in `cmd/replay`. Exported records, events and accounts contain only scalar value fields. The core copies input account configuration and returns copied slices/maps. Changing a snapshot cannot mutate the authoritative journal. Commands require a single serialized caller; this is an API guarantee, not a tamper-resistant audit store.

`New(accounts)` creates a ledger. `Post(event)` returns an error code or nil and appends a receipt plus its accepted effect or rejection. Declined authorizations also record their terminal decision. Event IDs are globally unique within an instance; authorization IDs are scoped to an account. Rejected IDs are consumed, so retries append duplicate errors without reapplying money.

`Finish()` closes through Day 6 and appends one interest credit per account. Repeating a successful finish does nothing; later posts append `WINDOW_FINALIZED` errors. `BalanceAt` and `AuthorizationsAt` expose historical sequence cutoffs; cutoff zero means current knowledge. `Accrued(account, day)` uses day zero for the total across the window.

Money enters as fixed decimal strings and is stored as `int64` minor units. `math/big` intermediates provide exact parsing, ties-to-even rounding and rational interest. Parsing and every affected value-date cash balance are checked for overflow. A derived close/restatement failure is returned explicitly; earlier appended effects remain and cannot be rolled back. A failed finish leaves the window unfinalized, and capitalization is prechecked for all accounts before any credit is appended. Callers must inspect errors rather than blindly retrying IDs.

Backdating triggers ascending assessment of already closed days; the current day is assessed at close. E7 leads to fees for Days 2, 4, and 5. E9 reverses principal without implicit fee refunds or rewritten authorization decisions. Interest is restated by appending deltas, and capitalization equals the exact sum of rounded daily accruals. These choices resolve unspecified behavior; see [AMBIGUITIES.md](AMBIGUITIES.md).

No BHD fee or exchange rate was supplied. A negative BHD close emits `OVERDRAFT_FEE_NOT_CONFIGURED` and completes without a fee debit: an exposed unsupported policy, not a zero-fee exemption. The supplied BHD scenario remains positive.

## Required documents

- [NUMBERS.md](NUMBERS.md) explains constants and their scale.
- [AMBIGUITIES.md](AMBIGUITIES.md) records monetary, timing and lifecycle decisions.
- [REJECTED.md](REJECTED.md) refuses criteria 2, 6, 7 and 8 and records abandoned approaches.
- [WORKLOG.md](WORKLOG.md) records actual timestamped work and validation.
- [deliberate_failure/automatic_expiry_test.go](deliberate_failure/automatic_expiry_test.go) contains the annotated failing test.

The repository is delivered as one clean Go commit, following the later instruction not to preserve intermediate commits. The work log records the actual build and corrections. Part 2 is a separate review draft; PDF conversion awaits approval.
