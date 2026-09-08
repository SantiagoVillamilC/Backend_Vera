---
name: golang-bootstrap
description: How a Go service on this stack (chi + pgx/sqlc against self-hosted PostgreSQL) boots and stops. Covers the composition root in `cmd/api/main.go` (a single `run() error` with one exit point, constructor wiring of the pool, the sqlc querier, river workers and chi resources), `http.Server` timeouts, the signal-driven graceful shutdown and its load-bearing order (HTTP first, background jobs second, pool last), boot-time configuration validation with a stdlib `LoadConfig()` that reports every missing or malformed variable at once through `errors.Join`, `/healthz` liveness versus `/readyz` readiness and why liveness touches no dependency, and background jobs on river with transactional enqueue (`InsertTx`) on the same pgx pool. Activate when writing or reviewing `main.go`, a `Config` or `LoadConfig`, a health probe, a shutdown path, a river worker or client, or whenever a service reads the environment, builds a pool, or exits anywhere other than the composition root. Not for routing, middleware, or the response envelope (see `chi-patterns`), not for pool sizing, queries, transactions, or Goose (see `sqlc-patterns`), not for general Go idioms, error wrapping, or context (see `golang-patterns`), and not for tests (see `golang-testing`).
---

# Go Bootstrap

The process boundary of a Go service: what runs before the first request and after the last one. Every sibling skill on the Go stack describes a layer that lives *inside* a running process; this one describes how that process is assembled, validated, probed, and torn down.

Versions are pinned in `version-standards`: Go **1.27.0**, river **v0.44.1** (the subpackages `riverdriver/riverpgxv5` and `rivermigrate` are tagged in lockstep, pin all three at the same version). Verified 2026-09-04 against `go doc` on go1.27.0 (`os/signal`, `net/http`), the pgx v5.10.0 source, and the river v0.44.1 source.

## Role and boundaries (which skill to use)

This skill = the composition root, boot-time config validation, health probes, and background jobs. Everything the root wires together already has an owner, so do not re-derive it here:

- **Repository and service layers** (the generated `Querier`, a service wrapping it), **query optimization**, **transactions**, **`NewPool` and its sizing**, **Goose migrations** → `sqlc-patterns`.
- **Routing, the middleware chain and its order, rate limiting, RBAC, request-scoped context, constructor injection on resource structs**, and the `{success, data, error, meta}` **envelope** (`WriteData` / `WriteError` / `WriteAPIError`) → `chi-patterns`.
- **Errors**: domain errors, `%w`, `errors.Is`/`errors.As`, `errors.Join` → `golang-patterns` (language level); turning one into an HTTP response → `chi-patterns` (HTTP level).
- **Token verification** (HS256 with the shared `JWT_SECRET`) → the service's own `internal/auth` module (`ValidateJWT` in `internal/auth/service.go`). The root only validates that the secret is present and long enough.
- **Tests** for anything the root constructs → `golang-testing`.

## When to Activate

- Writing or reviewing `cmd/api/main.go`, or any file that constructs a pool, a client, a server, or a worker.
- Adding an environment variable, or finding `os.Getenv` anywhere outside `LoadConfig`.
- Adding or reviewing `/healthz` or `/readyz`, or wiring them to a load balancer or orchestrator.
- Anything touching signals, `srv.Shutdown`, `pool.Close`, `riverClient.Stop`, or the order they run in.
- Adding a river job kind or worker, building the river client, or enqueueing a job from a service.

## Composition root (`cmd/api/main.go`)

One file wires the process. Nothing deeper in the tree calls `os.Getenv`, builds a pool, or calls `log.Fatal`. Per `golang-patterns`, `cmd/` exists only with two or more binaries; a single binary keeps this file at the module root as `main.go`, and the wiring is identical.

```go
func main() {
	if err := run(); err != nil {
		log.Fatalf("startup: %v", err) // the ONLY exit point in the program
	}
}

func run() error {
	cfg, err := LoadConfig() // first statement: fail fast, before anything is constructed
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop() // safety net for the early-return paths below

	pool, err := db.NewPool(ctx, cfg.DatabaseURL) // `sqlc-patterns`
	if err != nil {
		return fmt.Errorf("pool: %w", err)
	}
	defer pool.Close() // safety net for the early-return paths below; Close is idempotent

	queries := dbstore.New(pool) // sqlc-generated, satisfies dbstore.Querier
	// If the service adds a cache client later, it is built here too: same root, same rule.

	mailer := mail.New(cfg.SMTPURL) // worker dependencies are built here too, like handler ones
	riverClient, err := newRiverClient(pool, mailer)
	if err != nil {
		return fmt.Errorf("river: %w", err)
	}
	// context.Background(), NOT ctx: cancelling Start's context is a HARD stop that cancels
	// every running job's context (a soft stop only if Config.SoftStopTimeout is set). The
	// graceful, ORDERED shutdown goes through Stop() below, after the HTTP drain.
	if err := riverClient.Start(context.Background()); err != nil {
		return fmt.Errorf("river start: %w", err)
	}

	srv := &http.Server{
		Addr:    net.JoinHostPort("", cfg.Port),
		Handler: api.NewRouter(api.NewUsersResource(queries), api.NewHealthResource(pool)),
		// Zero falls back to ReadTimeout, and both zero means NO limit at all: this is the
		// Slowloris guard, and net/http will not set it for you.
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1) // buffered: the goroutine must never block once nobody is listening
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()
	slog.InfoContext(ctx, "listening", "addr", srv.Addr)

	select {
	case err := <-errCh: // the listener died on its own (port in use, fd exhaustion)
		return err
	case <-ctx.Done(): // SIGINT or SIGTERM
	}
	stop() // restore default signal handling NOW: a second Ctrl-C during a slow drain kills the process

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil { // 1. stop accepting, drain in-flight requests
		return fmt.Errorf("http shutdown: %w", err)
	}
	if err := riverClient.Stop(shutdownCtx); err != nil { // 2. stop fetching jobs, finish running ones
		return fmt.Errorf("river stop: %w", err)
	}
	pool.Close() // 3. last: steps 1 and 2 both still need connections
	return nil
}
```

- **`run() error`, not a body full of `log.Fatal`.** `Fatal` calls `os.Exit`, which skips every `defer`, so a pool or a river client leaks on any late failure path. One `Fatalf` in `main`, every other failure returns.
- **The root is the only place that knows the concrete graph.** Constructors take what they need as arguments (`NewUsersResource(queries)`, `newRiverClient(pool, mailer)`) and never reach for the environment themselves. A package that reads `os.Getenv` on its own cannot be handed a different config in a test and hides a dependency the compiler could otherwise check.
- **Shutdown order is load-bearing**: HTTP first, river second, pool last. This extends the rule in `sqlc-patterns` (server before pool): closing the pool while a handler or a job still holds a connection turns work in flight into connection errors. Steps 1 and 2 share the same 30s budget because they share `shutdownCtx`.
- **The shutdown context derives from `context.Background()`, never from `ctx`**, which the signal already cancelled. A timeout built on a cancelled parent is born expired: `Shutdown` still closes the listeners and idle connections, but returns `context.Canceled` without waiting for in-flight requests.
- **Call `stop()` as soon as the signal lands, not only in a `defer`.** `os/signal.NotifyContext` documents that further interrupts do not trigger the default exit behavior *until the returned stop function is called*. With `defer stop()` alone, a second Ctrl-C during a slow drain is swallowed until `run` returns. The `defer` stays as the safety net for the early-return paths; `stop` is safe to call twice.
- `srv.Shutdown` closes listeners, closes idle connections, and waits for active ones to become idle. It does **not** wait for hijacked connections such as WebSockets, and it does **not** wait for the callbacks registered with `srv.RegisterOnShutdown` either (it starts them with `go f()` and they must not block). Registering a notifier is therefore half of the job: a service that holds sockets keeps a registry of open connections with a `sync.WaitGroup` (`Add(1)` per registered connection, `Done()` when its handler returns), the notifier only tells that registry to close everything, and the sequence above gains a step **between 1 and 2**: call the registry's close once more synchronously (`Shutdown` can return before the notifier ran), then wait on that `WaitGroup` bounded by the same `shutdownCtx`, logging and continuing on timeout, all **before** `riverClient.Stop`, so no handler is still touching the pool when step 3 closes it. The registry, the handler side, and the exact `run()` delta are in `golang-websocket`.

## Config and env validation

Stdlib only, no env-parsing library. One `Config` struct, explicit `os.Getenv`, one `LoadConfig()` called on the first line of `run()`. A missing variable must stop the process at boot, not surface as a nil client three hours later.

```go
// cmd/api/config.go, package main
type Config struct {
	Port        string
	DatabaseURL string
	JWTSecret   string
	SMTPURL     string
	LogLevel    string
}

func LoadConfig() (*Config, error) {
	cfg := &Config{
		Port:        envOr("PORT", "8080"),
		DatabaseURL: os.Getenv("DATABASE_URL"),
		JWTSecret:   os.Getenv("JWT_SECRET"),
		SMTPURL:     os.Getenv("SMTP_URL"),
		LogLevel:    envOr("LOG_LEVEL", "info"),
	}

	var errs []error
	// A slice, not a map: deterministic error order. List only what THIS service cannot boot
	// without. SMTP_URL belongs here only in a service that actually uses it.
	required := []struct{ name, value string }{
		{"DATABASE_URL", cfg.DatabaseURL},
		{"JWT_SECRET", cfg.JWTSecret},
	}
	for _, r := range required {
		if r.value == "" {
			errs = append(errs, fmt.Errorf("%s is required", r.name))
		}
	}
	if p, err := strconv.Atoi(cfg.Port); err != nil || p < 1 || p > 65535 {
		errs = append(errs, fmt.Errorf("PORT must be 1..65535, got %q", cfg.Port))
	}
	// RFC 7518 section 3.2: an HS256 key MUST be at least the hash output size, 256 bits.
	// The message reports the length only: the value is a secret and must never reach a log.
	if cfg.JWTSecret != "" && len(cfg.JWTSecret) < 32 {
		errs = append(errs, fmt.Errorf("JWT_SECRET must be at least 32 bytes for HS256, got %d", len(cfg.JWTSecret)))
	}
	// Shape, not only presence, same as PORT: slog.Level parses debug/info/warn/error in any case,
	// and a typo must fail the boot here, not the first log line. Handler wiring is in `golang-logging`.
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(cfg.LogLevel)); err != nil {
		errs = append(errs, fmt.Errorf("LOG_LEVEL must be debug, info, warn or error, got %q", cfg.LogLevel))
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("config: %w", errors.Join(errs...))
	}
	return cfg, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
```

- **`errors.Join` reports every problem in one run.** Failing on the first sends an operator through N deploys to find N mistakes.
- **Validate shape, not only presence.** An unparseable `PORT` or a `JWT_SECRET` too short to key HS256 is as fatal as a missing one, and only the boot check catches it cheaply.
- **Never log the `Config` value.** `DATABASE_URL` carries the password, `JWT_SECRET` signs every session, and `SMTP_URL` may carry credentials too. Log the names that resolved, never the values, and never `%+v` the struct.
- **Defaults belong to optional variables only.** Never default `DATABASE_URL` to a localhost DSN: that turns a misconfigured production deploy into a silent connection to nothing.

## Health checks

Two probes with different jobs. `/healthz` answers "is this process alive" and touches nothing: if it pings Postgres, a brief database blip makes the orchestrator restart a perfectly healthy process. `/readyz` answers "can it serve traffic right now" and checks the dependencies whose absence would fail every request.

```go
// internal/api/health.go
type HealthResource struct {
	pool *pgxpool.Pool
}

func NewHealthResource(pool *pgxpool.Pool) HealthResource {
	return HealthResource{pool: pool}
}

// Liveness: no dependency touched, ever.
func (rs HealthResource) Live(w http.ResponseWriter, _ *http.Request) {
	WriteData(w, http.StatusOK, map[string]string{"status": "alive"}, nil)
}

// Readiness: dependencies, on a budget of its own.
func (rs HealthResource) Ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	checks := map[string]string{"postgres": "ok"}
	var failed []string
	if err := rs.pool.Ping(ctx); err != nil {
		checks["postgres"] = "unavailable"
		failed = append(failed, fmt.Sprintf("postgres: %v", err))
	}
	if len(failed) > 0 {
		slog.ErrorContext(ctx, "readiness check failed", "failed", failed) // detail stays server-side
		WriteError(w, http.StatusServiceUnavailable, "NotReady", "One or more dependencies are unavailable.", nil)
		return
	}
	WriteData(w, http.StatusOK, checks, nil)
}
```

This is the one resource that holds the raw `*pgxpool.Pool` instead of a `Querier`: it needs `Ping`, which the generated interface does not expose, and it never runs a query. Every other resource follows the `chi-patterns` rule and holds the interface.

Both probes go in the **public** `r.Group()` of `NewRouter`, next to `/login`. `chi-patterns` keeps auth non-global precisely so probes stay reachable; its `NewRouter` shows a single bare `health` handler in that group, and these two replace it:

```go
r.Group(func(r chi.Router) { // public, fresh stack, same path space
	r.Get("/healthz", health.Live)
	r.Get("/readyz", health.Ready)
})
```

- **`/readyz` must return 503, never a 200 carrying `"ready": false`.** Load balancers and orchestrators read the status code, not the body.
- **Its own 2s timeout.** Without one, a hung pool holds the probe open until the global `middleware.Timeout`, and the orchestrator gives up before the handler does.
- **`Ping` only, never a business query.** This runs every few seconds, forever, on every replica.
- **Failure detail is logged, not serialized.** A driver error can carry a host, a database name, or a DSN fragment; the response says which dependency is down and nothing else.

## Background jobs (river)

Go services use `github.com/riverqueue/river` **v0.44.1** (driver `riverdriver/riverpgxv5`, tagged in lockstep), not a Redis-backed queue such as asynq. One property decides it: the job row is written **inside the same Postgres transaction as the business write that schedules it**, so no crash can commit one without the other. River's own `InsertTx` doc states the guarantee: because of snapshot visibility across transactions, the job is not worked until the transaction commits, and if the transaction rolls back, so does the inserted job.

Schema first. Install the CLI at the pinned version and migrate as part of the deploy step, next to the goose run (`sqlc-patterns`):

```bash
go install github.com/riverqueue/river/cmd/river@v0.44.1
river migrate-up --database-url "$DATABASE_URL"
```

Job args plus worker. `river.JobArgs` requires exactly one method, `Kind() string`, and that string, not the Go type name, is what identifies queued rows across deploys:

```go
type SendWelcomeArgs struct {
	UserID uuid.UUID `json:"user_id"` // serialized with encoding/json: exported fields only
}

// Stable identifier: renaming it strands every job already queued under the old kind.
// Format without spaces (`send_welcome`, `send-welcome`); a rename across deploys needs
// river's JobArgsWithKindAliases, not a bare edit.
func (SendWelcomeArgs) Kind() string { return "send_welcome" }

type SendWelcomeWorker struct {
	river.WorkerDefaults[SendWelcomeArgs] // supplies Timeout/NextRetry/Middleware; you write Work
	mailer Mailer
}

func (w *SendWelcomeWorker) Work(ctx context.Context, job *river.Job[SendWelcomeArgs]) error {
	return w.mailer.SendWelcome(ctx, job.Args.UserID) // a returned error schedules a retry
}
```

Client, built on the **same pool** as everything else in the composition root:

```go
func newRiverClient(pool *pgxpool.Pool, mailer Mailer) (*river.Client[pgx.Tx], error) {
	workers := river.NewWorkers()
	river.AddWorker(workers, &SendWelcomeWorker{mailer: mailer}) // one call per worker type; a duplicate panics

	return river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues:  map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 100}},
		Workers: workers,
	})
}
```

`riverpgxv5.New(pool)` fixes the client's `TTx` to `pgx.Tx`, which is exactly why `InsertTx` accepts the transaction the service already opened.

Transactional enqueue, the whole reason river is here:

```go
func (s *UserService) Register(ctx context.Context, in RegisterInput) (dbstore.User, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return dbstore.User{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // `_ =` per sqlc-patterns: ErrTxClosed is the happy path

	user, err := dbstore.New(tx).CreateUser(ctx, in.ToParams())
	if err != nil {
		return dbstore.User{}, err
	}

	// Same tx: the job becomes visible to workers only once the commit lands,
	// and disappears with the row if it rolls back.
	if _, err := s.river.InsertTx(ctx, tx, SendWelcomeArgs{UserID: user.ID}, nil); err != nil {
		return dbstore.User{}, fmt.Errorf("enqueue welcome: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return dbstore.User{}, fmt.Errorf("commit: %w", err)
	}
	return user, nil
}
```

- **`Insert` (no `Tx`) runs on its own connection and reopens the exact gap river was chosen to close**: the row commits, the process dies, the job never existed. Any job triggered by a DB write uses `InsertTx`.
- The generic `WithTx` helper in `sqlc-patterns` hands its callback a `*dbstore.Queries`, not the `pgx.Tx`, so a flow that also enqueues opens the transaction inline as above (or widens that helper to pass the tx through).
- **Only the insert goes inside the transaction, never the work itself.** The transaction holds row locks for as long as it is open; the worker runs later, on its own connection, with its own context.
- **`Start` returns immediately and runs the fetch loop in a background goroutine**, so the composition root owns its lifecycle: a non-cancellable context in, `Stop(shutdownCtx)` on the way out. `Stop` stops fetching and waits for in-progress jobs; if `shutdownCtx` expires first it returns that context's error. `StopAndCancel`, or cancelling the context given to `Start`, is the abrupt variant that also cancels running jobs. `river.Config.SoftStopTimeout` (default none: wait indefinitely) turns every stop path into a soft stop that escalates to the hard one once the timeout elapses; river's doc for that field shows the shortcut of handing the signal context straight to `Start`, which this skill avoids because it would begin river's stop at the same instant as the HTTP drain instead of after it.

## Anti-Patterns

- **`log.Fatal` anywhere except `main`.** It skips every `defer`; the pool and the river client leak on the way out.
- **`os.Getenv` outside `LoadConfig`**, including a package-level `var cfg = mustLoadConfig()` that runs at import time before `main` can report anything.
- **Returning on the first missing variable.** Collect them and `errors.Join`.
- **`log.Printf("config: %+v", cfg)`.** The DSN and the JWT secret are in there.
- **A default for `DATABASE_URL`.** A default for `PORT` is fine; a default DSN hides a broken deploy.
- **`context.WithTimeout(ctx, ...)` for the shutdown budget** where `ctx` is the signal context. It is already cancelled.
- **`riverClient.Start(ctx)` with the signal context.** Every SIGTERM becomes a hard stop that cancels running jobs.
- **`defer stop()` as the only `stop()` call.** A second Ctrl-C during the drain is swallowed until `run` returns.
- **`/healthz` that pings Postgres.** A database blip restarts a healthy process.
- **`/readyz` returning 200 with a `"ready": false` body.** Nothing upstream reads the body.
- **A readiness probe with no timeout of its own**, or one that runs a business query instead of `Ping`.
- **`river.Insert` for a job caused by a database write.** Use `InsertTx` on the same transaction.
- **Running the job's work inside the transaction that enqueued it.** Locks are held for the duration of the work.
- **`go install .../cmd/river@latest`.** River is pre-1.0; a minor bump can break the API. Pin `@v0.44.1` like the three Go packages.

## Related Skills

- `chi-patterns` for `NewRouter`, the public `r.Group()` the probes live in, `WriteData` / `WriteError`, and the resource structs the root constructs and hands a `Querier`.
- `golang-websocket` for the connection registry (`sync.WaitGroup` plus a closing flag) that `RegisterOnShutdown` only notifies, the step between `srv.Shutdown` and `riverClient.Stop` that drains it, and the exact `run()` delta.
- `golang-logging` for the `slog.SetDefault` call that follows `LoadConfig()` in `run()` (before the pool, so the first boot line is already structured), the handler that stamps `request_id` from the chi context onto every `InfoContext`/`ErrorContext` record, `Config` as a `slog.LogValuer` (the type-level form of "never log the `Config` value"), the `LOG_FORMAT` field it adds to `Config`, and why `log.Fatalf` in `main` stays as it is.
- `sqlc-patterns` for `NewPool` and its sizing, the `Querier` handed to resources, the `WithTx` helper the enqueue example works around, and the Goose migrations that run next to `river migrate-up`.
- `golang-patterns` for `%w`, `errors.Join`, the context rules the shutdown path relies on, goroutine leaks (why `errCh` is buffered), and the package layout that decides whether `main.go` lives under `cmd/`.
- `golang-testing` for the `withTx` fixture that tests a service which enqueues, and `httptest` against the probes.
- `version-standards` for the pinned numbers quoted here (Go 1.27.0, river v0.44.1 and its two lockstep subpackages).
