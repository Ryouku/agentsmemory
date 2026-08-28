# Task ADR-042-T3: Read contributions from the authenticated Open Collective API

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `orderSource` interface, `providerOrder` struct, `newOCOrderSource(httpClient, apiURL, token, slug)`
**Consumes:** none
**Data dependency:** hermetic — tests run against recorded fixtures and an `httptest` server, never the live API

## Goal

Add a read-only seam that returns the collective's incoming orders as a provider-neutral struct,
so nothing downstream needs to know GraphQL.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/billing/provider.go` | edit | Declare `orderSource` beside the three existing seams, with a doc comment saying why this one is a READ (the other three are writes or verification). |
| `internal/billing/ocorders.go` | add | The GraphQL client and decoder. |
| `internal/billing/testdata/oc_orders_page.json` | add | Recorded response fixture, shaped from the live schema read 2026-08-28. Deliberately carries a null tier, a null email, null tags and a null nextChargeDate — the awkward shapes the API really produces. |
| `internal/billing/testdata/oc_orders_empty.json` | add | The zero-orders response — the case that must not read as an error. |
| `internal/billing/testdata/oc_orders_errors.json` | add | A GraphQL refusal: HTTP 200 carrying `errors` AND `data.account: null`. This is the shape that otherwise decodes to an empty page and reports success. |
| `internal/billing/ocorders_test.go` | add | Decoder + auth-header + error-vs-empty tests. |

## Ordered Steps

1. Write the failing tests first (TDD red): `TestOCOrderSourceDecodesAPage`,
   `TestOCOrderSourceDistinguishesEmptyFromError`, `TestOCOrderSourceSendsPersonalTokenHeader`,
   `TestOCOrderSourceNeverLogsTheToken`. Confirm RED.
2. Define `providerOrder`: `ID` (`legacyId` as string), `Status`, `Frequency`, `TierLegacyID`,
   `AmountValue`, `Currency`, `Tags []string`, `FromAccountSlug`, `FromAccountEmail`,
   `NextChargeDate`, `CreatedAt`. Every field maps to one confirmed schema field — do not invent
   fields the API does not expose.
3. Implement the query against `account(slug: $slug) { orders(limit, offset, filter: INCOMING) }`,
   held as ONE package-level constant so a schema change has exactly one place to be fixed.
4. Send `Personal-Token`; use the injected `*http.Client` so the caller owns the timeout.
5. Decode `errors[]` FIRST: a GraphQL 200 carrying `errors` is a failure, and treating it as an
   empty page is precisely the "empty result reads as an answer" defect this repo keeps finding.
   Return `(nil, err)`, never `(nil, nil)`.
6. Page with `offset` until fewer than `limit` come back; cap total pages and log when the cap bites
   rather than truncating silently.
7. Confirm GREEN.

## Acceptance

```bash
go test ./internal/billing/ -run 'TestOCOrderSourceDecodesAPage|TestOCOrderSourceDistinguishesEmptyFromError|TestOCOrderSourceSendsPersonalTokenHeader|TestOCOrderSourceNeverLogsTheToken' -count=1 2>&1 | tee /tmp/adr042-t3-new.out && \
! grep -qE "no tests to run|^FAIL|^--- FAIL|\[no tests to run\]" /tmp/adr042-t3-new.out && \
grep -q "^ok" /tmp/adr042-t3-new.out && \
go build ./... && go vet ./... && go test ./internal/billing/ -count=1
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestOCOrderSourceDecodesAPage` | `internal/billing/ocorders_test.go` | Fixture decodes to `providerOrder`s with tier id, status, tags and amount intact | — |
| `TestOCOrderSourceDistinguishesEmptyFromError` | `internal/billing/ocorders_test.go` | Zero orders returns `(empty, nil)`; a 200 carrying `errors[]` returns a non-nil error | — |
| `TestOCOrderSourceSendsPersonalTokenHeader` | `internal/billing/ocorders_test.go` | The `Personal-Token` header is present and carries the configured value | — |
| `TestOCOrderSourceNeverLogsTheToken` | `internal/billing/ocorders_test.go` | No error constructor contains the token — table-driven over ALL FOUR failure paths (graphql errors, HTTP status, nil account, undecodable body) | — |

**A mutation caught a real hole here too, recorded because the shape recurs.** The token-leak mutant
first SURVIVED: the test used a 401 carrying `errors[]`, and since the errors block is checked before
the status, the HTTP-status error constructor was never reached. A leak test that covers one of
several error constructors proves nothing about the others — the test is now table-driven over all
four, and the mutant is killed.

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The four tests above |
| 2 — something selects it | Nothing yet — T4 consumes it, T5 constructs it. This task deliberately ships an unreachable component, which is why it MUST NOT be marked done in isolation as evidence the feature works |
| 3 — the caller can discover it | Package-internal interface; `n/a: no declared external interface` |
| 4 — it is used | T5's reconcile log line reports the order count; nothing measures it before that |

## Mutation Log

- 2026-08-28 · a78864d* · mutant killed · exit 1 · `internal/billing/ocorders.go` · Stops checking the GraphQL errors[] block, so a refused query decodes to an empty page and reports success — a permissions failure would read as "nobody has paid" forever and no plan would ever activate. · acceptance-sha256:42ca0fa57407b1ba7858136a5d9f9656aa391525ae480f6d2c7b3ff223fdaeba
- 2026-08-28 · a78864d* · mutant killed · exit 1 · `internal/billing/ocorders.go` · Sends the query anonymously. The call still succeeds against a public collective, so nothing behavioural notices — only an explicit header assertion does, which is why that test exists. · acceptance-sha256:42ca0fa57407b1ba7858136a5d9f9656aa391525ae480f6d2c7b3ff223fdaeba
- 2026-08-28 · a78864d* · mutant survived · exit 0 · `internal/billing/ocorders.go` · Puts the read-only financial credential into an error string that the reconcile driver logs every pass, which would place it in every downstream log aggregator. · acceptance-sha256:42ca0fa57407b1ba7858136a5d9f9656aa391525ae480f6d2c7b3ff223fdaeba
  ```
  the fence passed with the mechanism broken
  ```
- 2026-08-28 · a78864d* · mutant killed · exit 1 · `internal/billing/ocorders.go` · Puts the read-only credential into an error the driver logs every pass. Previously SURVIVED: the leak test only exercised the errors[] path, which is checked BEFORE the status, so this constructor never ran. The test is now table-driven over all four failure paths. · acceptance-sha256:42ca0fa57407b1ba7858136a5d9f9656aa391525ae480f6d2c7b3ff223fdaeba

## Invariants

- The client only READS. No mutation is ever sent to Open Collective.
- A GraphQL `errors[]` is never swallowed into an empty page.
- The token never reaches a log line or an error string.
- No test in this task contacts the network.

## Risks

- The fixture is hand-shaped from a schema read rather than from a real order, because the project
  had zero orders on 2026-08-28. A real response may carry nulls the fixture does not. Mitigated by
  decoding defensively (pointer/omitempty for optional fields) and by T4's real-order sign-off,
  which is where a real payload is first seen.

## Stop Condition

If the `orders` connection turns out to require host-admin rather than collective-admin permission
for the token available, stop: the scope of the token is an operator decision and may change which
account slug we query.

## Out of Scope

- Mapping orders to plan changes — T4.
- Constructing the client from config — T5.

## Verification Log
- 2026-08-28 · a78864d* · exit 0 · `go test ./internal/billing/ -run 'TestOCOrderSourceDecodesAPage|TestOCOrderSourceDistinguishesEmptyFromError|TestOCOrderSourceSendsPersonalTokenHeader|TestOCOrderSourceNeverLogsTheToken' -count=1 2>&1 | tee /tmp/adr042-t3-new.out && \ …` · acceptance-sha256:42ca0fa57407b1ba7858136a5d9f9656aa391525ae480f6d2c7b3ff223fdaeba
