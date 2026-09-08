---
name: sqlc-patterns
description: Default data-access patterns for the Go stack: sqlc (SQL-first codegen) on pgx v5 against self-hosted PostgreSQL, with Goose for migrations. Covers sqlc.yaml and the generate workflow, pgxpool sizing and self-signed SSL, :one/:many/:exec queries and the generated Querier interface, pgx.Tx transactions, SQLSTATE error mapping with pgerrcode, cursor pagination, and the full forward-only Goose migration lifecycle (expand-and-contract, NO TRANSACTION for CONCURRENTLY, and the mark-as-applied gap). Activate for sqlc queries, codegen, the pgx pool, Go transactions, Goose migrations, or Go database work in general.
origin: ECC
---

# sqlc Patterns

The default data-access layer for the Go stack. **sqlc is the entry point for all application-level database work in Go**, start here. This skill targets **sqlc v1.31.x** generating **pgx v5** code against **self-hosted PostgreSQL** (no managed service), with **Goose v3.27.x** for migrations.

Confirm the versions with `sqlc version && goose -version && go list -m github.com/jackc/pgx/v5` before applying version-specific patterns. Verified 2026-08-23: sqlc `v1.31.1`, pgx `v5.10.0`, goose `v3.27.3` (needs Go 1.25+), `github.com/jackc/pgerrcode v0.0.0-20250907135507-afb5586c32a6` (no tagged release exists, pin the pseudo-version).

## Role and boundaries (which skill to use)

This skill = application layer (queries, codegen, the pool, transactions) **plus the Goose migration lifecycle**. Route elsewhere for:

- **Index *type* decisions, EXPLAIN/ANALYZE, tuning, roles, locks** to `postgres-patterns`. You *write* `CREATE INDEX` in a migration here; you pick *which kind* there (§1), the `max_connections` math there (§3), lock and deadlock behavior there (§5), safe DDL and `lock_timeout` there (§6). That skill is written behind a Prisma app, but those sections are engine-level and language-agnostic, so the routing holds.
- **Caching, rate limiting, queues** to `redis-patterns`.
- **Migrations live HERE, not in `database-migrations`.** That skill scopes itself to Prisma Migrate explicitly, exactly as `drizzle-patterns` owns drizzle-kit. Do not send a Goose question there, and do not assume `migrate resolve` has an equivalent (it does not, see below). The shared *principles* are restated here against Goose commands.

## When to Activate

- Writing or changing `.sql` query files, or running `sqlc generate`
- Standing up or tuning the `pgxpool` connection pool
- Transactions, retries, or mapping database errors to domain errors
- Any schema change: new table, column, index, backfill, zero-downtime rename
- Pagination over a growing table

## Project layout and `sqlc.yaml`

sqlc never runs migrations. It reads the Goose migration directory as the **schema source of truth** (it parses goose, atlas, dbmate, golang-migrate, sql-migrate and tern files, and it detects and ignores Down sections).

```
db/migrations/     # goose, FLAT: goose does not walk subdirectories (issue #951)
db/queries/        # sqlc, walks recursively, nest freely
internal/dbstore/  # generated: db.go, models.go, querier.go, users.sql.go
```

```yaml
version: "2" # the only accepted value; a missing version or an empty `sql` list is a hard error
sql:
  - engine: postgresql
    schema: db/migrations
    queries: db/queries
    gen:
      go:
        package: dbstore
        out: internal/dbstore
        sql_package: pgx/v5                   # short string: pgx/v4 | pgx/v5 | database/sql
        sql_driver: github.com/jackc/pgx/v5   # FULL module path, and required for :copyfrom
        emit_interface: true                  # generates Querier in querier.go
        emit_json_tags: true
        emit_empty_slices: true               # :many returns [] instead of nil
        wrap_errors: true                     # fmt.Errorf("query GetUser: %w", err)
```

- `sql_package` and `sql_driver` take **different forms** (short string vs full module path) and are trivially inverted.
- The YAML decoder runs with `KnownFields(true)`: **any unknown or misspelled key is a hard parse failure**, never silently ignored.
- `wrap_errors` wraps with `%w`, so `errors.Is(err, pgx.ErrNoRows)` and `errors.As(err, &pgErr)` still work, and every error names its query.
- Migration files are parsed **lexicographically**: unpadded prefixes (`1_`, `2_`, `10_`) silently build the wrong schema with no error, so use timestamps or zero-padded numbers.
- Types: nullable columns become `pgtype.Text` / `pgtype.Timestamptz` / `pgtype.Numeric` (`emit_pointers_for_null_types: true` swaps those for `*T`), and `uuid` becomes `pgtype.UUID` unless you add `overrides: [{column: "*.uuid", go_type: "github.com/google/uuid.UUID"}]`.

```bash
go install github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1
sqlc generate                            # after every query or migration change
sqlc generate && git diff --exit-code    # in CI: fails when the committed code is stale
# commit the generated package: it is build input reviewers must see, not a build artifact
```

## The pool (pgxpool, self-hosted, long-running)

```go
func NewPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn) // NewWithConfig PANICS on a struct-literal Config
	if err != nil { return nil, fmt.Errorf("parse dsn: %w", err) }
	cfg.MaxConns = 20                               // default is max(4, runtime.NumCPU())
	cfg.MinIdleConns = 5                            // floor on IDLE conns, cuts tail latency
	cfg.MaxConnLifetime = 45 * time.Minute          // default 1h
	cfg.MaxConnLifetimeJitter = 5 * time.Minute     // default 0, avoids a recycle stampede
	cfg.MaxConnIdleTime = 5 * time.Minute           // default 30m
	cfg.ConnConfig.ConnectTimeout = 5 * time.Second // else the pool floors it at 2 minutes
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil { return nil, fmt.Errorf("new pool: %w", err) }
	return pool, pool.Ping(ctx)
}
```

- **One pool per process**, built at boot and passed down, never one per request. `pool.Stat()` is the hook for pool metrics (acquired, idle, total, wait count). Every knob also exists as a DSN parameter (`pool_max_conns`, `pool_min_idle_conns`, `pool_max_conn_lifetime`, ...) if sizing belongs in config rather than code.
- `MinConns` floors *total* pool size; `MinIdleConns` floors *idle* connections and is the one that keeps warm connections around. Sizing: 25 to 40 `MaxConns` with `MinIdleConns` at 20 to 30 percent is commonly-cited guidance, not a verified rule. **The rule is the arithmetic**: `(MaxConns x app instances) + migration/admin connections` must stay below `max_connections - superuser_reserved_connections` (`postgres-patterns` §3). PgBouncer in transaction mode only once that stops fitting.
- Shutdown order: stop accepting requests first (`srv.Shutdown(ctx)`), then `pool.Close()` once, which blocks until every connection is returned. Closing it with requests in flight turns them into connection errors.

### SSL against a self-hosted server with a self-signed certificate

Express TLS **entirely in the DSN**, never by patching `cfg.ConnConfig.TLSConfig` after `ParseConfig`: `prefer`, `allow` and multi-host DSNs run through fallback entries a hand-patched `TLSConfig` does not reach, so you can silently connect in plaintext.

```bash
# Preferred: server cert with a SAN matching the DSN host, CA distributed to clients.
DATABASE_URL="postgres://app:pw@db.internal:5432/app?sslmode=verify-full&sslrootcert=/etc/ssl/certs/pg-ca.crt"
# SAN cannot match the host (bare IP, MagicDNS)? verify-ca: chain checked, hostname not.
# CA in the OS trust store? sslrootcert=system, which also FORCES verify-full regardless of sslmode.
```

**Bare `sslmode=require` with no `sslrootcert` sets `InsecureSkipVerify = true`**: encrypted, zero certificate verification, MITM-able. It is not the answer for a self-signed certificate. Adding `sslrootcert` to a `require` DSN silently upgrades it to `verify-ca` behavior (deliberate libpq parity). One DSN serves app and migrations: the goose CLI's postgres driver **is** `github.com/jackc/pgx/v5/stdlib`, so it parses the same string with the same TLS semantics and harmlessly ignores the `pool_*` parameters.

## Core Concepts

### Query annotations and named parameters

```sql
-- name: GetUser :one
SELECT * FROM users WHERE id = $1 AND deleted_at IS NULL;
-- name: ListUsersByOrg :many
SELECT id, email, created_at FROM users
WHERE org_id = @org_id AND created_at < @before ORDER BY created_at DESC, id DESC;
-- name: DeactivateStale :execrows
UPDATE users SET active = false WHERE last_seen_at < $1;   -- returns int64, not the rows
```

| Annotation | Returns | Use for |
|---|---|---|
| `:one` | one struct, `pgx.ErrNoRows` if absent | lookup by primary key or unique column |
| `:many` | `[]struct` (empty slice with `emit_empty_slices`) | lists |
| `:exec` | `error` only | writes whose row count you do not need |
| `:execrows` | `int64` affected | bulk update/delete where the count is the answer, and `:copyfrom` for bulk COPY inserts |

Positional `$1` is fine for one or two parameters. Past that use **named parameters** (`@org_id`, or `sqlc.narg('x')` when nullable): the generated `Params` struct gets real field names instead of `Column1`, and reordering the SQL stops silently reordering the arguments.

### The `Querier` interface

`emit_interface: true` generates `Querier` covering every query; `dbstore.New(pool)` returns `*Queries`, which implements it. **Accept the interface, return the struct** (the Go principle, see `golang-patterns`): depend on `Querier` so a service is testable with a fake and never imports the pool. Generated row types are database shapes, so map them to a DTO at the service boundary exactly as with any ORM entity.

```go
type UserService struct{ q dbstore.Querier } // interface, not *dbstore.Queries, and never the pool
func (s *UserService) Get(ctx context.Context, id uuid.UUID) (*User, error) {
	row, err := s.q.GetUser(ctx, id)
	if err != nil { return nil, mapDBError(err) }
	return toDTO(row), nil // never return the generated row struct from a handler
}
```

### Transactions

`*Queries` has `WithTx(tx)`, and `dbstore.New` accepts anything satisfying `DBTX`, so a `pgx.Tx` works directly.

```go
func WithTx[T any](ctx context.Context, pool *pgxpool.Pool, fn func(*dbstore.Queries) (T, error)) (T, error) {
	var zero T
	tx, err := pool.Begin(ctx)
	if err != nil { return zero, fmt.Errorf("begin: %w", err) }
	// `_ =` deliberately: after a successful Commit this Rollback returns pgx.ErrTxClosed and that
	// is the expected path. A bare `defer tx.Rollback(ctx)` is flagged by errcheck (`golang-patterns`).
	defer func() { _ = tx.Rollback(ctx) }()
	out, err := fn(dbstore.New(tx))
	if err != nil { return zero, err } // deferred Rollback runs
	if err := tx.Commit(ctx); err != nil { return zero, fmt.Errorf("commit: %w", err) }
	return out, nil
}
```

- **No HTTP, email, payment or any other network call inside a transaction.** It holds locks for the remote peer's latency, which is how lock pileups and deadlocks happen.
- Use the `tx` querier for every statement in the closure: the pool-backed querier there silently runs outside the transaction, on another connection.
- On `40001` / `40P01`, retry the **whole** transaction with jittered backoff. Retrying a statement inside the aborted transaction only returns `25P02` (`in_failed_sql_transaction`).

## Code Examples

### Error handling: SQLSTATE to domain errors

```go
// pgconn from github.com/jackc/pgx/v5/pgconn, NOT standalone jackc/pgconn (errors.As never matches that type)
func mapDBError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) { return ErrNotFound } // pgx.ErrNoRows, NOT sql.ErrNoRows
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case pgerrcode.UniqueViolation:
			if pgErr.ConstraintName == "users_email_key" { return ErrEmailTaken } // which index
			return ErrConflict
		case pgerrcode.ForeignKeyViolation: return ErrInvalidReference
		case pgerrcode.NotNullViolation, pgerrcode.CheckViolation: return ErrValidation
		case pgerrcode.SerializationFailure, pgerrcode.DeadlockDetected: return ErrRetryable
		}
	}
	return err
}
```

| SQLSTATE | `pgerrcode` constant | Maps to | Retry? |
|---|---|---|---|
| `23505` | `UniqueViolation` | conflict (switch on `ConstraintName` for the precise rule) | No |
| `23503` | `ForeignKeyViolation` | invalid reference (insert/update), still referenced (delete) | No |
| `23502` / `23514` | `NotNullViolation` / `CheckViolation` | validation, a caller bug | No |
| `23P01` | `ExclusionViolation` | overlapping range | No |
| `40001` / `40P01` | `SerializationFailure` / `DeadlockDetected` | transient | Yes, whole tx, jittered backoff |
| `55P03` | `LockNotAvailable` | `lock_timeout` hit | Yes, with backoff |
| `57014` | `QueryCanceled` | `statement_timeout` or context cancel | Treat as deadline exceeded |
| `53300` | `TooManyConnections` | pool or server sizing problem | Back off, do not hammer |
| `42xxx` | `SyntaxError`, `UndefinedTable`, ... | programmer error, 500 | Never |

Prefer the class predicates (`IsIntegrityConstraintViolation`, `IsConnectionException`, `IsTransactionRollback`) when only the family matters, and log `pgerrcode.Name(pgErr.Code)` so the constant name appears instead of a bare number.

### Cursor pagination

```sql
-- name: ListPostsPage :many
SELECT id, title, created_at FROM posts
WHERE published = true AND deleted_at IS NULL
  AND (sqlc.narg('after_created_at')::timestamptz IS NULL
       OR (created_at, id) < (sqlc.narg('after_created_at')::timestamptz, sqlc.narg('after_id')::uuid))
ORDER BY created_at DESC, id DESC   -- id breaks ties, so pages are stable
LIMIT sqlc.arg('page_size')::int;
```

```go
rows, err := s.q.ListPostsPage(ctx, dbstore.ListPostsPageParams{ // cur is the zero value on page 1:
	AfterCreatedAt: cur.CreatedAt, AfterID: cur.ID, // not-valid pgtype values no-op the predicate
	PageSize: int32(limit + 1)}) // one extra row
if err != nil { return nil, mapDBError(err) }
hasNext := len(rows) > limit
if hasNext { rows = rows[:limit] } // pop the probe row
```

Fetching `limit + 1` and popping detects `hasNext` with no `COUNT(*)`. The row-value comparison `(created_at, id) < (...)` is one index-friendly predicate, unlike the hand-written `OR` chain. Offset pagination only for admin tables that jump to an arbitrary page.

## Migrations with Goose (owned by this skill)

`database-migrations` is Prisma-only by its own scope statement. The principles carry over, the commands do not.

1. **Every change is a migration.** Never alter production by hand.
2. **Forward-only in production.** `goose down` is a local convenience; to revert in prod, ship a new forward migration. Never run `down`, `reset` or `down-to 0` against production.
3. **Immutable once applied.** Goose does **not** checksum files, so editing an applied one fails *silently*: production keeps the old schema while `sqlc generate` regenerates Go code from the edited file, describing a schema no deployed database has.
4. **Schema and data migrations are separate** (never a large `UPDATE` in a migration file), and both are **tested against production-sized data**: what is instant on 100 rows locks on 10M.

```bash
export GOOSE_DRIVER=postgres GOOSE_MIGRATION_DIR=db/migrations
export GOOSE_DBSTRING="$DATABASE_URL"    # same DSN as the pool, same pgx TLS semantics
goose create add_user_display_name sql   # UTC timestamp version; -s for 00001_ sequential
goose status                             # applied vs pending
goose up                                 # apply all pending (up-by-one / up-to VERSION also exist)
goose validate                           # parse-check every file, no DB writes
goose fix                                # ONE TIME before a version ships: dev timestamps to sequential
                                         # never on every CI run: it renames already-applied files
goose down                               # LOCAL ONLY
```

Out-of-order (missing) migrations are a hard error unless you pass `-allow-missing`. `-table` (default `goose_db_version`) takes `schema.table` for a non-public schema, and `.env` loading is on by default (disable with `-env=none`). Run migrations as **one dedicated step** (a CI job or a one-shot container), never from every replica's entrypoint: `goose_db_version` has no unique constraint on `version_id`, so concurrent runners can both apply and both record. To run them from Go instead, bridge the pool with `stdlib.OpenDBFromPool(pool)` (goose wants a `*sql.DB`), then `goose.SetBaseFS(embedFS)` + `goose.SetDialect("postgres")` + `goose.Up(db, "migrations")`. Only apply operations work on an embedded FS; `create` and `fix` still need the OS filesystem.

### Zero downtime: expand and contract

Rename `full_name` to `display_name` with no downtime (about 3 deploys). Migrations run **before** the code that needs them, and the new column starts nullable so old code keeps working.

1. `goose create add_display_name sql` with `ALTER TABLE users ADD COLUMN display_name text;` (nullable, no table rewrite), then `goose up`.
2. Deploy code that **writes both** columns (dual-write). Run `sqlc generate` so the new column exists in the models.
3. Backfill in batches with a **script**, not a migration:
   ```go
   for {
       n, err := q.BackfillDisplayName(ctx, 5000) // :execrows, WHERE display_name IS NULL LIMIT ...
       if err != nil { return err }
       if n == 0 { break }
       time.Sleep(200 * time.Millisecond) // let replication and autovacuum breathe
   }
   ```
4. `ALTER TABLE users ALTER COLUMN display_name SET NOT NULL;` in a migration, once the backfill reports zero remaining.
5. Deploy code that **reads** `display_name` only, then drop `full_name` in a final migration. Prefix heavy DDL with `SET lock_timeout = '5s';` so a blocked `ALTER` fails fast instead of queueing behind every reader (`postgres-patterns` §6).

### CONCURRENTLY and the NO TRANSACTION directive

Goose wraps each migration file in a transaction, and `CREATE INDEX CONCURRENTLY` cannot run inside one:

```sql
-- +goose NO TRANSACTION
-- +goose Up
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_posts_created_at ON posts (created_at DESC);
-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS idx_posts_created_at;
```

- The annotation line must have **no leading whitespace** before `--` (a leading space or tab is a parse error); casing is not significant. It disables the transaction for the **entire file**, `Up` and `Down` both.
- **Goose has no dirty/failed state** (issue #728, open). Without a transaction, a failure at statement N leaves statements 1..N-1 permanently applied and writes **no** row to `goose_db_version`, so the next `goose up` replays the file from the top (a plain `INSERT` gets duplicated every retry). So: **exactly one statement per NO TRANSACTION file**, idempotent where possible (`IF NOT EXISTS`). A failed `CREATE INDEX CONCURRENTLY` also leaves an **INVALID** index that must be dropped by hand before retrying.

### Goose has no `migrate resolve` equivalent

Verified negative as of **v3.27.3**: goose has **no** `stamp`, `force`, `repair`, `resolve` or mark-as-applied command of any kind. Issue #938 is open (filed 2025-04-13) and PR #954 adding `stamp` / `stamp-to` is open and unmerged. Do not invent the flag. `-no-versioning` is **not** that feature and must never be reached for as a substitute: it applies migrations *without recording* them, the exact inverse of recording without applying.

The only workaround is a manual write, which the issue author himself calls unsafe. Back up first, wrap it in a transaction, verify with `goose status` afterwards:

```sql
-- goose_db_version(id, version_id bigint, is_applied bool, tstamp timestamp)
INSERT INTO goose_db_version (version_id, is_applied) VALUES (20260101120000, true); -- mark applied
DELETE FROM goose_db_version WHERE version_id = 20260101120000;                      -- un-mark
```

Consequence for hotfixes: make the migration itself idempotent (`IF NOT EXISTS`, `ADD COLUMN IF NOT EXISTS`) so re-running it is harmless, instead of applying SQL by hand and then reconciling a registry that has no repair command.

## Anti-Patterns

| Anti-pattern | Why it fails | Instead |
|---|---|---|
| HTTP/email call inside a `pgx.Tx` | Holds locks for the remote peer's latency | Commit first, then call, compensate on failure |
| Looping a `:one` query per id (N+1) | One round trip per row | One `:many` with `WHERE id = ANY(@ids::uuid[])` |
| Ignoring `pgx.ErrNoRows`, or checking `sql.ErrNoRows` | pgx uses its own sentinel, so `:one` misses become 500s | `errors.Is(err, pgx.ErrNoRows)` to `ErrNotFound` |
| Editing an applied migration | Goose does not checksum: silent schema divergence | New forward migration |
| Multi-statement `NO TRANSACTION` file | Partial apply, nothing recorded, replayed next `up` | One idempotent statement per file |
| `sslmode=require` for a self-signed cert | `InsecureSkipVerify = true`, MITM-able | `verify-full` + `sslrootcert` (`verify-ca` only if the SAN cannot match) |
| `goose up` from every replica's entrypoint | Concurrent runners race an unconstrained version table | One dedicated migration step |
| Returning generated row structs from a handler | Leaks column names and internal fields | Map to a DTO at the service boundary |

## Best Practices

| Rule | Reason |
|---|---|
| `ParseConfig`, mutate, then `NewWithConfig` | A struct-literal `Config` makes `NewWithConfig` panic |
| Size the pool by arithmetic, not by a blog number | `(MaxConns x instances) + admin < max_connections - reserved` |
| Accept `Querier`, return concrete structs | Testable services, no pool import in the domain layer |
| Commit generated code, `git diff --exit-code` in CI | Stale codegen caught at PR time, not at runtime |
| Backfills as scripts, DDL as migrations | Long `UPDATE`s lock and cannot resume |
| Back up before any manual `goose_db_version` write | There is no `resolve` command to undo it |

## Related Skills

- `postgres-patterns` engine layer: index types (§1), tuning and `max_connections` (§3), locks and deadlocks (§5), safe DDL and `lock_timeout` (§6)
- `redis-patterns` caching, rate limiting, queues
- `golang-patterns` Go idioms this layer plugs into: accept interfaces and return structs, error wrapping, context propagation
- `chi-patterns` router, middleware, and the response envelope these domain errors map into
- `backend-integration-testing` testing against a real Postgres test database instead of faking `Querier`
- `golang-testing` table-driven tests, fakes, and coverage for the service layer
