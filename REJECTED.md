# Refused criteria and abandoned approaches

The criteria below are refused. Criteria 2 and 6 expose underspecified fee policy; the implementation's chosen policy is recorded in `AMBIGUITIES.md`. They are not rejected by claiming that append-only storage inherently forbids refunds.

## Criterion 2: “E7 causes exactly one overdraft fee to be assessed, on Day 2.”

**Refused under the implemented historical reassessment policy.** A late posting changes every subsequent value-date balance. With E6 rejected, principal balances immediately after E7, before any fee, are:

| Day | Principal balance, AED | Calculation |
| --- | ---: | --- |
| 2 | -370.00 | 1,200.00 - 950.00 - 620.00 |
| 3 | 30.00 | -370.00 + 400.00 |
| 4 | -155.00 | 30.00 - 185.00 |
| 5 | -155.00 | No additional accepted cash movement |

Days are evaluated in ascending order, and fees affect later balances. Day 2 receives AED 25.00, leaving AED -395.00; Day 3 then closes at AED 5.00 and needs no fee. Day 4 is AED -180.00 after that earlier fee and receives a second AED 25.00, leaving AED -205.00. At the Day 5 close it receives a third AED 25.00, leaving AED -230.00. The three fees have value dates Day 2, Day 4, and Day 5 and are observed Day 5. E7 causes two immediate historical assessments and one assessment at the next actual close.

“Once per day per account” limits duplicate fees for the same day. It does not limit the number of affected days to one. A model that froze past closes would require a different explicit rule; it would not justify choosing only Day 2 while ignoring the negative Day 4 and Day 5 closes.

## Criterion 6: “After E9, all balances and fees return to their pre-E7 values.”

**Refused under the implemented retained-fee policy.** E9 appends a credit that offsets E7's principal, but no fee refund event or refund rule is supplied. E7, E9, the assessed fees, the declined Auth-B decision, and their audit records all remain. Before E7, Day 2 through Day 5 principal value-date balances were AED 250.00, 650.00, 465.00, and 465.00. After E9, the retained fees leave those respective ledger closes at AED 225.00, 625.00, 415.00, and 390.00. Fees total AED 75.00; the ordinary Day 6 close before interest capitalization is AED 390.00.

Append-only accounting can support fee refunds with new compensating entries. It does not permit deleting old fee records, and it does not decide refund eligibility. Automatically issuing refunds would add an unsupplied business policy. Historical daily interest is corrected by append-only adjustment records to reflect the final balances; authorization decisions are not replayed into different outcomes. Thus even a future refund policy would not literally erase all intervening state.

## Criterion 7: “The three BHD instalments in E10 must each be BHD 3.334.”

**Refused because it creates money.** `3 × 3.334 = 10.002`, exceeding the BHD 10.000 credit by BHD 0.002. Perfect equality cannot be represented at three decimal places for this total and count. The implemented split is BHD 3.334, 3.333, and 3.333. The pieces differ by at most BHD 0.001 and sum exactly to BHD 10.000; the first instalment receives the indivisible remaining minor unit.

## Criterion 8: “If the rounded daily interest accruals do not sum to the capitalized total, the remainder is discarded.”

**Refused because it contradicts exact conservation.** The capitalization is constructed from the sum of the rounded daily amounts; it is not separately rounded from unrounded aggregate interest. Original accruals plus their signed adjustments give each final daily amount, and those amounts sum exactly to the single capitalization credit. Discarding a remainder would credit a different amount from the amount accrued and violate the explicit rule.

For the final restated AED closes, the six daily amounts are AED 0.10, 0.09, 0.25, 0.17, 0.16, and 0.16, totaling AED 0.93. BHD accrues 0.004 on Day 5 and 0.004 on Day 6, totaling BHD 0.008. There is no discarded residual.

## Criteria accepted

| Criterion | Resolution |
| --- | --- |
| 1: Day 2 is AED -370.00 at end of Day 5 before any fee | Accepted as the principal-only, pre-assessment counterfactual. The actual post-assessment Day 2 balance is AED -395.00. |
| 3: Accept Auth-A's Day 4 settlement | Accepted: Auth-A was active and AED 185.00 is within its AED 200.00 hold. |
| 4: Reject an unknown authorization settlement without debiting funds | Accepted: E6 produces `UNKNOWN_AUTHORIZATION` and no cash posting. |
| 5: An approved Auth-B affects available balance, not ledger cash | Accepted and tested in a separately funded scenario. In the supplied replay Auth-B is declined, so there is no active hold. |

## Approaches abandoned or evaluated

- **Actually implemented, then abandoned: independently round each equal instalment.** The initial Python prototype used `round(Fraction(total, count))` for every piece. For E10 this produced `(3333, 3333, 3333)` minor units and lost one thousandth. The conservation test failed with `9999 != 10000`, as recorded at the actual observation time in `WORKLOG.md`. The replacement uses integer quotient and remainder, giving the earliest pieces one extra minor unit until the remainder is exhausted. This was a real intermediate defect, not a retrospective hypothetical.
- **Actually implemented, then replaced at the user's request: Python runtime and retained intermediate commits.** The first implementation and its passing suite used Python's standard library. The user subsequently requested Go and then explicitly requested that commits not be preserved. The current deliverable is a Go standard-library core with the same documented economic policies, published as a clean single commit. Superseded Python files and intermediate commits are not part of the final repository; `WORKLOG.md` records the real sequence of work and observations.
- **Actually implemented, then corrected: advance time after checking only booking date.** The initial core could close days and assess fees for a command with an invalid value date, even though that command was subsequently rejected. The clock gate now validates both dates and their ordering before advancing. A regression test confirms that an attempted Day 6 credit with value date Day 7 cannot close Day 1 or charge its overdrawn account. Validly dated business rejections still represent advancing time, as documented in `AMBIGUITIES.md`.
- **Evaluated and rejected: sort by booking date.** That would move E10 ahead of E9 and lose the requested input-order audit. The implemented replay retains both supplied booking time and monotonic observed time.
- **Evaluated and rejected: float money or independent aggregate-interest rounding.** Floats cannot exactly represent many decimal amounts. Separately rounding unrounded aggregate interest can differ from the sum of rounded daily accruals. Integer minor units and an exact rational rate avoid both problems. These alternatives were not committed implementations.
- **Evaluated and rejected: edit historical cash, fees, or interest records on correction.** This violates the journal contract. Reversals and interest corrections instead append records. A fee refund remains a possible future append-only command; it is outside the chosen policy.
- **Evaluated and rejected: retroactively re-decide authorizations.** That would turn a real approval and accepted settlement into a different history after E7 arrives. Approval and lifecycle records remain decisions made with then-known information.
- **Evaluated and rejected: treat an unknown settlement as an ordinary debit.** That would allow E6 to move money despite its missing authorization. The error remains auditable and funds do not leave.

## Deliberately failing test of this design

The repository contains one inline-annotated, opt-in failing test against the current design, rather than breaking the normal acceptance suite: `deliberate_failure/automatic_expiry_test.go`, under the `designgap` build tag. Run `go test -tags designgap ./deliberate_failure`; it exits nonzero because the actual state is `ACTIVE` while the assertion requires `EXPIRED`. The test is neither skipped nor treated as an expected failure. It demonstrates the absence of automatic authorization expiry: elapsed days alone do not end an active hold. The supported `EXPIRE` command requires an explicit external decision. The failure documents a production limitation, not an acceptance criterion silently ignored by the implementation.
