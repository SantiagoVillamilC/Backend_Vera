---
name: golang-testing
description: Go testing patterns for this stack: standard `go test`, table-driven subtests, and hand-written fakes over mocking libraries. Covers the Go form of the RED-GREEN-REFACTOR gate (a compile failure counts as RED), t.Helper and t.Cleanup, the stdlib-plus-go-cmp assertion position vs testify, fakes and function-type adapters before gomock/moq, net/http/httptest, integration tests against a real self-hosted Postgres via testcontainers-go with transaction-rollback and snapshot-restore isolation, -race and -cover wired into CI with a real threshold gate, and testing.B benchmarks with b.Loop. Activate when writing or reviewing any `_test.go` file, deciding how to fake a dependency in Go, standing up a Go test database, or wiring `go test` into CI. Not for chi routing and middleware (see `chi-patterns`), not for query construction or migrations (see `sqlc-patterns`), and not for general Go idioms (see `golang-patterns`).
origin: ECC
---

# Go Testing

Closes a real gap: `rules/ecc/golang/testing.md` is four lines. It says "use table-driven tests",
prints `go test -race ./...` and `go test -cover ./...`, then points here. It never says what a
table-driven test looks like, how to fake a dependency, how a Go test gets a real database, or how
a coverage number becomes a CI gate. Verified against **go1.27.0** (2026-08-19); under the
two-newer-majors policy only 1.27.x and 1.26.x are supported.

## Role and boundaries

Owns **test structure, fakes, test-database lifecycle and isolation, and the `go test` CLI surface**
(race, coverage, benchmarks). Does not own:

- TDD rationale, coverage target, git checkpoints → `tdd-workflow`. This file translates that cycle
  into Go, it does not restate it.
- The "real DB, never a faked data layer" doctrine and two-tier isolation → `backend-integration-testing`.
  Same doctrine, Postgres and Go instead of Prisma and Vitest.
- Router setup, middleware order, endpoint assertion checklist → `chi-patterns`.
- sqlc codegen, the `Querier`/`DBTX` interfaces, pgxpool, Goose → `sqlc-patterns`.
- Error wrapping, context, concurrency primitives, package layout → `golang-patterns`.

## When to Activate

- Writing or reviewing any `_test.go` file, including a benchmark or an example function.
- Deciding how to stand in for a dependency: hand-written fake, function adapter, or codegen.
- Standing up a test database, or choosing the isolation tier for a store, service or HTTP test.
- Wiring `go test` into CI: `-race`, `-cover`, `-coverpkg`, the scoped 80% gate, benchmark runs.
- Diagnosing a flaky test, a `t.Parallel()` failure, or a race the detector reports (or misses).

## RED-GREEN-REFACTOR in Go

Same gate as `tdd-workflow`, with one translation that matters: **a test that does not compile is a
valid RED.** `tdd-workflow` blesses this as "compile-time RED", and in Go it is the normal case: a
test referencing an unwritten function fails at build time, not at runtime.

```bash
go test -run TestQuote ./internal/billing   # RED: undefined: Quote  → commit `test: add reproducer for quote`
go test -run TestQuote ./internal/billing   # GREEN after minimal impl → commit `fix: quote pro plan`
go test -race ./...                         # REFACTOR gate: whole suite green under -race
```

The compile failure must name the missing or wrong **production** symbol. A build failure caused by
a typo in the test file is not RED, it is a broken test. Do not edit production code until the
failure points at production code.

## 1. Table-driven tests

One test function, a table of cases, one `t.Run` subtest per case. Prefer a **map** keyed by the
case name over a slice with a `name` field: shorter, a duplicate name becomes a compile-time
mistake, and Go's random map iteration order surfaces order-dependence instead of hiding it.

```go
func TestTotal(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		items    []int
		discount int
		want     int
		wantErr  bool
	}{
		"no discount":      {items: []int{100, 200}, discount: 0, want: 300},
		"ten percent off":  {items: []int{100, 200}, discount: 10, want: 270},
		"empty basket":     {items: nil, discount: 50, want: 0},
		"discount too big": {items: []int{100}, discount: 101, wantErr: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := Total(tc.items, tc.discount)
			if (err != nil) != tc.wantErr {
				t.Fatalf("Total(%v, %d) error = %v, wantErr %v", tc.items, tc.discount, err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("Total(%v, %d) = %d, want %d", tc.items, tc.discount, got, tc.want)
			}
		})
	}
}
```

Naming: `TestFunc`, or `TestFunc_Condition` when one function needs several test functions. Subtest
names are lowercase prose ("discount too big"), not method names; Go rewrites spaces to underscores
in the reported path, so `-run 'TestTotal/empty_basket'` selects one case. `t.Fatalf` aborts the
subtest (when continuing would panic or assert on garbage), `t.Errorf` records and continues.

## 2. Test helpers and assertions

`t.Helper()` on the first line of any helper that can fail: it drops the helper's frame from the
reported location, so the failure points at the calling test line.

```go
func mustQuote(t *testing.T, b *Billing, id string) Invoice {
	t.Helper()
	inv, err := b.Quote(t.Context(), id)
	if err != nil {
		t.Fatalf("Quote(%q): unexpected error: %v", id, err)
	}
	return inv
}
```

Teardown goes in `t.Cleanup(fn)`, not `defer`: cleanups run LIFO after the test **and** after its
parallel subtests, compose across nested helpers, and still run when the test calls `t.Fatal`.
`t.TempDir()` and `t.Setenv()` register their own. `t.Context()` (Go 1.24+) is cancelled *just
before* cleanups run, so a cleanup needing a context must use `context.Background()`.

### Position: stdlib comparisons plus go-cmp, not testify

**Default:** bare `if got != want { t.Errorf(...) }` for scalars, `github.com/google/go-cmp` v0.7.0
`cmp.Diff` for structs, slices, and maps. Rationale: no test-only sub-language between the reader
and the test, and a diff naming the differing field instead of dumping both values.

```go
if diff := cmp.Diff(want, got); diff != "" {
	t.Errorf("Quote() mismatch (-want +got):\n%s", diff)
}
```

**Trap:** `cmp.Diff` **panics** on unexported fields, deliberately. Escape hatches, most common
first: `cmp.AllowUnexported(T{})`, `cmpopts.IgnoreUnexported(T{})`, or an `Equal` method on the
type. Also `cmpopts.EquateErrors()` for error trees, `cmpopts.SortSlices(...)` for unordered
results. go-cmp is tests-only: panic-on-ambiguity is a feature there, an outage in production.

**Exception:** a codebase already standardized on `stretchr/testify` stays on testify. Team
convention beats individual style and a migration buys nothing. testify v1.12.1 is maintained, but
its API is frozen at v1 by project policy, which is the honest reason not to pick it greenfield.
Inside a testify codebase: `require` for preconditions where continuing is meaningless (it calls
`t.FailNow`, illegal from a non-test goroutine), `assert` when you want every failure in one run.

## 3. Mocking: hand-written fakes first

Go interfaces are satisfied implicitly, so a fake is just a struct with the right method set: no
registration, no library, no codegen step. **For interfaces of roughly one to three methods this is
the default, not a fallback.**

```go
type fakeUserStore struct {
	users map[string]User
	err   error // set to force the failure path
}

func (f *fakeUserStore) ByID(_ context.Context, id string) (User, error) {
	if f.err != nil {
		return User{}, f.err
	}
	u, ok := f.users[id]
	if !ok {
		return User{}, ErrNotFound
	}
	return u, nil
}
```

Declare the interface in the **consumer** package listing only the methods that package calls, and
the fake stays small however wide the real implementation is. That is also why `sqlc-patterns` has
services accept `Querier` rather than the pool.

For a **single-method** interface skip the struct entirely: use a function-type adapter, the
`http.HandlerFunc` pattern applied to your own interface.

```go
type TokenValidator interface{ Validate(token string) (string, error) }

type TokenValidatorFunc func(string) (string, error)

func (f TokenValidatorFunc) Validate(token string) (string, error) { return f(token) }

// in a test, no struct declaration at all:
v := TokenValidatorFunc(func(string) (string, error) { return "", ErrExpired })
```

### When codegen earns its place

Two cases only: the interface is wide enough that hand-stubbing is real work, or the test must
assert on **interaction** (call ordering, counts, argument matchers) rather than returned values.
Then use `go.uber.org/mock` v0.6.0.

```bash
go install go.uber.org/mock/mockgen@latest
mockgen -typed -destination=internal/billing/mock_store_test.go -package=billing . UserStore
```

- `github.com/golang/mock` (Google's original) has been **archived** since 2024. Migrate any import
  of it to `go.uber.org/mock`, same API.
- Always pass `-typed`. It is still opt-in, and the untyped default gives `Return`/`Do`/`DoAndReturn`
  `interface{}` parameters, discarding compile-time safety on the expectations themselves.
- Use **package mode** or archive mode. Reflect mode is gone, superseded by package mode.
- `matryer/moq` v0.7.1 generates code that reads like a hand-written fake: a struct with function
  fields (`ByIDFunc func(...)`) instead of an `EXPECT()` DSL. moq for stubs, gomock for interaction
  assertions. Both are maintained; pick on style, not on health.

## 4. HTTP handlers with httptest

`httptest.NewRequest` plus `httptest.NewRecorder` invokes the handler directly: no port, no
listener, no goroutine. Use `httptest.NewServer` only when the code under test needs a real URL (an
outbound client, a redirect chain, TLS).

```go
req := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"ada"}`))
req.Header.Set("Content-Type", "application/json")
rec := httptest.NewRecorder()

CreateUser(rec, req)

if rec.Code != http.StatusCreated {
	t.Fatalf("status = %d, want %d", rec.Code, http.StatusCreated)
}
```

Routing, URL params, middleware order, and the endpoint assertion checklist (happy, validation,
auth, 404, conflict) belong to `chi-patterns`. Whenever a URL param or a middleware is part of the
behavior under test, drive the router there rather than the bare handler.

## 5. Integration tests against real Postgres

Same doctrine as `backend-integration-testing`: **a test that validates persistence runs against a
real Postgres.** A fake `Querier` reimplements query semantics by hand and diverges silently from
real constraints, cascades, unique violations, and transaction behavior, so it stays green while
production breaks. Do not substitute SQLite either: this stack uses Postgres-specific features with
no SQLite equivalent.

**Recommendation: `github.com/testcontainers/testcontainers-go` v0.44.0** with `modules/postgres`,
whose Postgres module ships wait strategies and snapshot/restore so neither gets hand-rolled.
`github.com/ory/dockertest/v4` v4.0.0 is a live, maintained alternative (stable major in April 2026,
rewritten on the Moby client for a lighter dependency tree), reasonable when dependency weight
matters or v3 code already exists. **Pin testcontainers-go exactly**: it is still pre-1.0, so a
minor bump can break the API, and read the release notes before bumping.

One container per test package, started in `TestMain`, migrations applied once:

```go
var (
	testPool *pgxpool.Pool
	pgCtr    *postgres.PostgresContainer
)

func TestMain(m *testing.M) {
	ctx := context.Background()
	var err error

	pgCtr, err = postgres.Run(ctx, "postgres:16-alpine", // Run, NOT the deprecated RunContainer
		postgres.WithDatabase("app_test"),
		postgres.WithUsername("test"), postgres.WithPassword("test"),
		postgres.WithSQLDriver("pgx"),
		postgres.BasicWaitStrategies(), // replaces hand-rolled readiness polling
	)
	must(err, "start postgres")

	dsn, err := pgCtr.ConnectionString(ctx, "sslmode=disable")
	must(err, "connection string")
	testPool, err = pgxpool.New(ctx, dsn)
	must(err, "pool")
	must(applyMigrations(ctx, dsn), "migrate") // once per package, never per test
	must(pgCtr.Snapshot(ctx), "snapshot")      // the clean baseline Restore returns to

	code := m.Run()
	testPool.Close()
	_ = testcontainers.TerminateContainer(pgCtr) // a deferred call would NOT run: os.Exit skips defers
	os.Exit(code)
}

func must(err error, what string) {
	if err != nil {
		log.Fatalf("%s: %v", what, err)
	}
}
```

`applyMigrations` is your Goose runner (`sqlc-patterns`), invoked against the container DSN.

`postgres.RunContainer` is documented deprecated and slated for removal in the next major, so code
written against it needs migrating.

### Isolation: two tiers, two techniques

**Tier 1, store and service tests: transaction-per-test rollback (default).** Nothing ever commits,
so parallel tests never see each other's writes and there is no cleanup query at all.

```go
func withTx(t *testing.T) *dbstore.Queries {
	t.Helper()
	tx, err := testPool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	// context.Background(), not t.Context(): t.Context() is already cancelled by cleanup time.
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	return dbstore.New(tx) // sqlc's DBTX is satisfied by both *pgxpool.Pool and pgx.Tx
}
```

This works because `sqlc-patterns` already has services accept `Querier` rather than the pool, and
`chi-patterns` holds a `Querier` on the resource struct rather than the pool. A service or handler
typed against `*pgxpool.Pool` cannot use this tier until it is widened. **Trap:** if the method
under test opens its own transaction it cannot run inside the outer test transaction. The seam is
`dbstore.New(DBTX)`, satisfied by both `*pgxpool.Pool` and `pgx.Tx`: have the method accept an
already-open `*dbstore.Queries` (or `Querier`) instead of calling `Begin` itself, and let
`sqlc-patterns`' `WithTx` helper be the production caller that opens and commits the transaction
around it. `WithTx` itself calls `pool.Begin`, so it is the wrapper you push outward, not the seam
you reuse inside the test.

**Tier 2, full HTTP tests: snapshot restore or truncation (fallback).** A test driving the real
server through its own pool has no test-controlled `tx`, so reset state instead: `pgCtr.Restore(ctx)`
in `t.Cleanup`, or `TRUNCATE TABLE users, orders RESTART IDENTITY CASCADE`. Restore drops and
recreates the database, so it tolerates no live connections to the target; close or recreate the
pool around it, otherwise truncation is simpler. Tier 2 runs **serially against one database**, and
`go test` runs separate packages in parallel by default, so use `go test -p 1 -tags=integration ./...`.
Gate the tier behind `//go:build integration` so plain `go test ./...` stays fast and Docker-free.

## 6. Race detector and coverage

Run `-race` as its own CI job rather than bolting it onto the coverage run. `-covermode` flips to
`atomic` whenever `-race` is set, and the flag docs call atomic counting "significantly more
expensive" than the default `set` mode, so a combined run pays for both instrumentations at once
and learns nothing the two separate runs do not.

```yaml
- run: go test -race -count=1 ./...
- run: go test -coverprofile=cover.out -coverpkg=./... ./...
```

`-count=1` defeats the test result cache, which otherwise reports a cached pass without ever having
run the detector.

**The `-race` false negative that matters:** it is a *dynamic* detector, reporting only races on
code paths that actually executed and actually interleaved during that run. A race in an untested
branch is invisible, and a test with a single goroutine, or with a `time.Sleep` that accidentally
serializes the interleaving, passes forever while the race is real. Raise the odds with
`t.Parallel()`, `-count=N` to reshuffle scheduling across runs, and `-cpu=1,4` where behavior
depends on `GOMAXPROCS`. A green `-race` run is evidence, not proof.

Coverage flags the rules layer omits:

- `-coverpkg=./...` is required when a test in one package exercises code in another; without it
  that package reports 0%. This hits integration tests directly, where a `store_test` drives store
  and handler packages that have no tests of their own.
- `-covermode` defaults to `set`, and to `atomic` automatically when `-race` is on. Set `atomic`
  explicitly only when counting statements in concurrent code without `-race`.

### The 80% gate, scoped

Go ships **no coverage threshold flag**, so the gate is a script. Before writing it, scope the
denominator: per `rules/ecc/common/testing.md` the 80% floor applies **only to domain logic and
services**. On the Go side that means excluding sqlc output (`*.sql.go` and the generated
`Querier`), `cmd/**/main.go`, generated mocks, and migration code. Those are never coverage gaps
and never get a test written to move the number.

Two mechanisms, and they are not interchangeable:

**Package-level scoping, `-coverpkg`.** It takes a comma-separated list of *package* patterns, not
file globs. It removes whole packages from the denominator, which is enough for `cmd/**`:

```bash
go test -coverprofile=cover.out \
  -coverpkg=./internal/domain/...,./internal/service/... ./...
```

**File-level filtering of the profile.** `-coverpkg` cannot help with `*.sql.go`, because sqlc
output sits in the *same package* as the hand-written store code you do want measured. Filter the
profile instead, then let `go tool cover -func` recompute the total over what is left:

```bash
awk 'NR==1 || !/\.sql\.go:|_mock\.go:|\/mocks\/|\/migrations\/|\/cmd\/[^:]*\/main\.go:/' \
  cover.out > cover.domain.out

go tool cover -func=cover.domain.out | awk '/^total:/ {
  gsub(/%/, "", $3)
  if ($3 + 0 < 80) { printf "coverage %s%% is below the 80%% gate\n", $3; exit 1 }
}'
```

`NR==1` is load-bearing: line 1 of a profile is `mode: set` (or `atomic`), and `go tool cover`
rejects a profile without it. Every other line is
`<import-path>/<file>.go:<line>.<col>,<line>.<col> <numstmt> <count>`, so the exclusions are plain
substring matches on the path. Verified on go1.27.0: filtering a demo profile moved the reported
total from 33.3% to 60.0%, i.e. the excluded files leave the denominator rather than counting as
zeroes.

Do not "fix" a red gate by adding a test for a generated file. Either the domain code is genuinely
under-tested, or the file belongs on the filter list above.

## 7. Benchmarks

Go-native, with no equivalent in the Node skills. A `Benchmark*` function in a `_test.go` file, run
only when `-bench` asks for it.

```go
func BenchmarkNormalize(b *testing.B) {
	input := strings.Repeat("  MixedCase  ", 8) // setup: excluded from the measurement
	b.ReportAllocs()

	for b.Loop() {
		Normalize(input)
	}
}
```

**Use `for b.Loop()`, not `for i := 0; i < b.N; i++`** (Go 1.24+). Never both in one benchmark.

- `Loop` resets the timer on its first call and stops it when it returns false, so setup before the
  loop and cleanup after it are excluded. **`b.ResetTimer()` after setup is no longer needed.**
- It blocks dead-code elimination by keeping call arguments, call results, and variables assigned
  inside the loop alive, so the old package-level sink variable is obsolete. Two constraints: the
  condition must be written **exactly** as `b.Loop()`, and only statements syntactically between the
  loop's braces are protected.
- The function body runs once per measurement instead of being re-run through `b.N` ramp-up, so
  expensive setup happens once.
- `b.StopTimer()` / `b.StartTimer()` are still required for per-iteration setup **inside** the loop
  body, the canonical case being refilling a slice before each sort.
- After `Loop` returns false, `b.N` holds the total iteration count, so `b.ReportMetric(v, "unit")`
  still computes custom averages. `b.ReportAllocs()` is the per-benchmark form of `-benchmem`.

```bash
go test -run='^$' -bench=. -benchmem -count=10 ./...   # -run='^$' matches no test, so only benchmarks run
```

`-bench` runs nothing by default and `-bench=.` runs everything; the regexp splits on unbracketed
`/` to select sub-benchmarks. `-benchtime` takes a duration (`1s` default, `-benchtime 1h30s`) or
the `Nx` form (`-benchtime 100x`) for an exact iteration count. `-count=10` produces the repeated
samples `benchstat` needs to call a difference significant. A single run is noise, never quote it
as a regression.

## Anti-Patterns

**`time.Sleep` as synchronization.** Sleeping 100ms then asserting is a flake on a loaded CI runner
and dead wall-clock time everywhere else. Signal instead: a channel the goroutine closes, a
`sync.WaitGroup`, or a cancellable context unwound by `t.Cleanup`. For genuinely time-dependent
logic (timeouts, retries, tickers) use `testing/synctest` (GA in Go 1.25, `synctest.Sleep` added in
1.27), which runs the bubble on a fake clock and makes the test both instant and deterministic.

**Shared mutable test state.** A package-level `var` mutated by tests, a fixture struct reused
across subtests, or `TestMain` seed data that later tests edit in place. It produces failures that
reproduce only in CI, only in that order. Build state per test with a helper returning a fresh
value, and let `t.Cleanup` unwind it.

**Unsafe `t.Parallel()`.** Three distinct traps:

- **Loop-variable capture.** Per-iteration loop variables landed in Go 1.22, so `tc := tc` is now
  redundant. But the semantics are gated on the `go` directive **in go.mod**, not on the installed
  toolchain: a module declaring `go 1.21` still gets the old shared variable under go1.27.0
  (verified locally). Check go.mod, not `go version`, before deleting the copy.
- **Cleanup ordering.** Parallel subtests pause and resume only after the parent test function
  returns, so a `defer` or parent-level `t.Cleanup` fires *before* they run. Register cleanup inside
  each parallel subtest, or wrap the group in a non-parallel parent.
- **Process-global state.** `t.Setenv` panics in a parallel test by design. Working directory, a
  global logger, or a shared container is the same hazard without the panic, and
  `testing.AllocsPerRun` also panics under parallel tests as of Go 1.25.

**Asserting on unexported internals or on log output.** Test exported behavior. A test reaching
into a private field pins the implementation and blocks the REFACTOR step it should protect.

## Related Skills

- `golang-patterns` for Go idioms, error wrapping, context, and concurrency in the code under test
- `chi-patterns` for router setup, middleware, and the endpoint assertion checklist behind section 4
- `sqlc-patterns` for the `Querier`/`DBTX` interfaces and pgxpool that make Tier 1 rollback possible
- `backend-integration-testing` for the real-DB doctrine and two-tier isolation this section translates
- `tdd-workflow` for the RED-GREEN-REFACTOR cycle, git checkpoints, and the scoped 80% gate
  enforced above; `rules/ecc/common/testing.md` owns the canonical exclusion list it is scoped by
