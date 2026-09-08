---
name: golang-logging
description: Structured logging for a Go service on this stack (chi v5 + sqlc/pgx against self-hosted PostgreSQL) with stdlib `log/slog`, no third-party logger. Covers the handler choice (`slog.NewJSONHandler` in production, `slog.NewTextHandler` in development, selected by `LOG_FORMAT`), mapping the existing `Config.LogLevel` string onto `slog.Level` through its `UnmarshalText` and validating it at boot inside `LoadConfig`, the single `slog.SetDefault` call in the composition root right after `LoadConfig()` so even the first boot line is structured (and the stdlib `log` package, `log.Fatalf` in `main` included, is bridged through the same handler), a wrapping `slog.Handler` that reads chi's request id from the context in `Handle` and stamps every record made with `InfoContext`/`ErrorContext` inside a request with `request_id`, making `Config` a `slog.LogValuer` so no slog call can ever print `DATABASE_URL` or `JWT_SECRET`, an access log and a panic log on chi's `RequestLogger` extension point so those two lines are JSON too, and `sloglint` inside the existing golangci-lint config. Activate when adding or reviewing any log call, choosing or configuring a handler, wiring `slog.SetDefault`, adding a `LogValue` method, correlating log lines with a request, replacing `middleware.Logger`, or enabling `sloglint`. Not for where `LoadConfig` and `run()` live or the rest of boot and shutdown (see `golang-bootstrap`), not for the chain's order or `RequestID` itself (see `chi-patterns`), not for context rules or the `.golangci.yml` file (see `golang-patterns`), and not for tests (see `golang-testing`).
---

# Go Logging

Every log line a Go service on this stack emits goes through one `slog.Handler`, chosen once in the composition root. The package is stdlib `log/slog` (Go 1.21+, announced at go.dev/blog/slog in August 2023), designed as a common `Handler` contract so libraries and applications interoperate, not as a forced replacement for third-party loggers. The announcement takes no position on performance against `zap` or `zerolog`, and the question does not arise here: a four-person team, one backend, no known extreme volume. Stdlib, without ambiguity.

Verified 2026-09-04 against `go doc` on go1.27.0 (`log/slog`: `Handler`, `Record.AddAttrs`, `Record.Clone`, `Level.UnmarshalText`, `LogValuer`, `SetDefault`, `DiscardHandler`), the chi v5.3.2 source (`middleware/logger.go`, `middleware/recoverer.go`), and golangci-lint v2.13.1 config validation with `sloglint` enabled. The request-id handler, `AccessLogger`, `LogValue`, `newHandler` and the `LoadConfig` validation lines below compiled, passed `go vet`, ran against the real `middleware.RequestID` and `Recoverer`, and are clean under the `sloglint` rules at the end; the `Config` and `run()` blocks are deltas with `// ...` elisions and were not compiled as shown.

## Role and boundaries (which skill to use)

This skill = which handler, at what level, how a record gets its `request_id`, what a `Config` prints, and the linter that keeps call sites in shape. The places it touches already have owners:

- **`run()`, `Config`, `LoadConfig`, and the exact spot `slog.SetDefault` occupies** → `golang-bootstrap`. This skill states two deltas to that file (a `LOG_FORMAT` field and the `SetDefault` line) the way `golang-websocket` states its `run()` delta; it does not restate the root.
- **`middleware.RequestID` first in the chain, `middleware.GetReqID`, the middleware order, `ClientIPFrom*`** → `chi-patterns`. This skill reads the id; it never produces it.
- **Context rules** (`ctx` first parameter, never stored in a struct, values are request-scoped metadata only) and **the `.golangci.yml` file itself** → `golang-patterns`. This skill adds one linter and its settings block to that file; it does not copy the file.
- **Tests** for anything here → `golang-testing`.

## When to Activate

- Writing or reviewing any `slog.*` call, or finding `log.Printf` / `fmt.Println` used as logging anywhere below `main`.
- Choosing, building, or wrapping a `slog.Handler`; touching `LOG_LEVEL` or `LOG_FORMAT`.
- A log line that must be traced back to one HTTP request, or a request whose lines cannot be found.
- A struct that holds a credential, a token, or a DSN and can reach a log call.
- Replacing `middleware.Logger`, or a panic that reached stderr as a raw stack trace instead of a record.
- Enabling or tuning `sloglint`.

## Handler and level

Two handlers, one switch. Production wants one JSON object per line (the container runtime, the log shipper, and `jq` all parse it); development wants the `key=value` text a human reads in a terminal. Both take the same `HandlerOptions`, so the choice is a single `if`.

`Config` in `golang-bootstrap` already carries `LogLevel string` with the default `"info"`. It gains one sibling and both get validated, in the same `errs` slice and under the same rule as `PORT`: shape, not only presence.

```go
// cmd/api/config.go: the logging fields of the Config in `golang-bootstrap`.
type Config struct {
	// ... Port, DatabaseURL, JWTSecret, SMTPURL exactly as in golang-bootstrap
	LogLevel  string // LOG_LEVEL,  default "info": debug | info | warn | error, any case
	LogFormat string // LOG_FORMAT, default "json": json | text
}

// In the literal inside LoadConfig:
//	LogLevel:  envOr("LOG_LEVEL", "info"),
//	LogFormat: envOr("LOG_FORMAT", "json"),

// After the JWT_SECRET length check, before `if len(errs) > 0`:
var lvl slog.Level
if err := lvl.UnmarshalText([]byte(cfg.LogLevel)); err != nil {
	errs = append(errs, fmt.Errorf("LOG_LEVEL must be debug, info, warn or error, got %q", cfg.LogLevel))
}
if cfg.LogFormat != "json" && cfg.LogFormat != "text" {
	errs = append(errs, fmt.Errorf("LOG_FORMAT must be json or text, got %q", cfg.LogFormat))
}
```

The handler is built from that `Config`, so its constructor lives next to `LoadConfig`, in package `main`:

```go
// cmd/api/logging.go
func newHandler(cfg *Config) slog.Handler {
	var lvl slog.Level
	_ = lvl.UnmarshalText([]byte(cfg.LogLevel)) // cannot fail: LoadConfig already rejected anything unparseable
	opts := &slog.HandlerOptions{Level: lvl}
	if cfg.LogFormat == "text" {
		return slog.NewTextHandler(os.Stderr, opts)
	}
	return slog.NewJSONHandler(os.Stderr, opts)
}
```

And the delta to `run()` in `golang-bootstrap` is one line, in one place:

```go
func run() error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	// Before the pool, before the signal context: from here on every line is structured,
	// including the errors this function returns to main.
	slog.SetDefault(slog.New(logging.WithRequestID(newHandler(cfg))))
	slog.Info("config loaded", "config", cfg) // safe: Config is a LogValuer, see below

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	// ... exactly as in golang-bootstrap
}
```

- **`LOG_FORMAT` defaults to `json` because the safe default is the production one.** A variable missed in a production deploy yields parseable lines; only a developer sets `text`, on purpose, in a shell. The same reasoning `golang-bootstrap` applies to `DATABASE_URL` (no default) and `PORT` (a harmless one).
- **`slog.Level` parses its own names, in any case.** `Level.UnmarshalText` accepts what `MarshalText` produces, ignoring case, so `INFO`, `Info` and `info` all resolve; `verbose` or an empty string fail, and `LoadConfig` turns that into a boot error next to the others instead of a first log line at the wrong level. `HandlerOptions.Level` takes any `Leveler`, and a plain `slog.Level` is one. A nil `Level` means `LevelInfo`, which is what the `"info"` default says explicitly.
- **`SetDefault` immediately after `LoadConfig()`, before the pool.** Everything after that line is structured, the `pool:` and `river:` errors returned to `main` included. It also re-routes the stdlib `log` package's default logger through the same handler (its doc: "output from the log package's default Logger (as with log.Print, etc.) will be logged using l's Handler"), which is why `log.Fatalf("startup: %v", err)` in `main` stays as it is: for every failure after `SetDefault` (pool, river, listener, shutdown) it emits one JSON record, then exits. Two limits, stated so nobody overclaims: a `LoadConfig` failure happens before `SetDefault` and prints plain text, fine for a line only a human at a terminal reads; and bridged `log` lines land at INFO, the `slog.SetLogLoggerLevel` default, so that fatal record says `"level":"INFO"`. Leave it: raising that level would raise every third-party `log.Print` with it.
- **`os.Stderr`**, matching the stdlib default and keeping stdout free.
- `AddSource` stays off. Turn it on in the `text` branch if file:line helps while developing; it costs a `runtime.Callers` per record.

## Correlation with the chi request id

`chi-patterns` puts `middleware.RequestID` first in the chain precisely so every log line can carry the id, and `middleware.GetReqID(ctx)` returns it anywhere downstream (an empty string outside a request). Typing `"request_id", middleware.GetReqID(r.Context())` by hand on every call is the wrong way to use that: it needs an `r` in scope, and it is forgotten on the one line that mattered. The `log/slog` package documentation describes the right way: "Some handlers may wish to include information from the context.Context that is available at the call site. One example of such information is the identifier for the current span when tracing is enabled." and "It is recommended to pass a context to an output method if one is available." So a `Handler` stamps the id, and the programmer's only duty is to pass `ctx`.

```go
// internal/logging/handler.go
package logging

import (
	"context"
	"log/slog"

	"github.com/go-chi/chi/v5/middleware"
)

// requestIDHandler adds the chi request id found in the context to every record.
type requestIDHandler struct {
	slog.Handler // embedded: Enabled is promoted unchanged
}

// WithRequestID wraps h so every record logged with a request context carries
// the id chi's RequestID middleware stored there, under the key "request_id".
func WithRequestID(h slog.Handler) slog.Handler {
	return requestIDHandler{Handler: h}
}

func (h requestIDHandler) Handle(ctx context.Context, r slog.Record) error {
	if id := middleware.GetReqID(ctx); id != "" {
		r.AddAttrs(slog.String("request_id", id))
	}
	return h.Handler.Handle(ctx, r)
}

// WithAttrs and WithGroup must re-wrap. The inner handler returns a NEW inner handler;
// returning it bare would hand Logger.With / WithGroup a handler that no longer injects
// the id. Embedding alone promotes the inner methods and does exactly that, silently.
func (h requestIDHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return requestIDHandler{Handler: h.Handler.WithAttrs(attrs)}
}

func (h requestIDHandler) WithGroup(name string) slog.Handler {
	return requestIDHandler{Handler: h.Handler.WithGroup(name)}
}
```

Inside a request, with `X-Request-Id: req-abc-123` on the wire (or the id chi generated):

```go
slog.InfoContext(ctx, "plain")
// {"time":"...","level":"INFO","msg":"plain","request_id":"req-abc-123"}
slog.With("k", "v").InfoContext(ctx, "after With")
// {"time":"...","level":"INFO","msg":"after With","k":"v","request_id":"req-abc-123"}
slog.ErrorContext(ctx, "unhandled request error", "err", err)
// {"time":"...","level":"ERROR","msg":"unhandled request error","err":"boom","request_id":"req-abc-123"}
```

- **Pass `ctx`, never store a logger in it.** The stdlib evaluated putting a `*slog.Logger` inside `context.Context` and removed it ("Ultimately, we removed the feature as being too controversial", go.dev/blog/slog). The pattern is `InfoContext` / `WarnContext` / `ErrorContext` with the context the call already has: `r.Context()` in a handler, the `ctx` parameter in a service or a repository. `sloglint`'s `context: scope` (below) turns a `slog.Info` with a `ctx` in scope into a lint error, so this is enforced, not remembered.
- **`WithAttrs` and `WithGroup` re-wrap**, per the comment in the code. Verified both ways: with the re-wrap, `slog.With(...)` and `Logger.WithGroup(...)` keep injecting; with embedding alone they stop.
- **`AddAttrs` on the value copy, no `Clone`.** The slog handler guide's rule for copies: a handler may make a single copy of a `Record` by assignment, channel send or function call if it does not retain the original, and needs `Record.Clone` only when its actions result in more than one copy. Here one record comes in by value, one goes out by value, nothing is retained.
- **The id follows the innermost open group.** A logger built with `Logger.WithGroup("g")` prints `"g":{"inner":1,"request_id":"..."}`, because attributes added to a record land in the group that is open. When a group is wanted, pass a `slog.Group("g", ...)` attribute on the call instead of building a `WithGroup` logger; the id stays top-level.
- **Outside a request the attribute is simply absent.** `GetReqID` returns `""` for the signal context in `run()`, for a river worker's context, for `context.Background()`; the record is written without `request_id` and nothing else changes. The handler is safe as the one and only default.
- **The manual attribute in `chi-patterns` becomes redundant.** Its `WriteAPIError` passes `"req_id", middleware.GetReqID(r.Context())` by hand; with this handler installed, the same id would print twice under two keys. Drop the manual pair the next time that snippet is touched; the handler's key is `request_id`.

## Config as a LogValuer

`golang-bootstrap` says "never log the `Config` value" in prose. `log/slog` lets the type say it: a value implementing `LogValuer` decides what slog prints for it, at every call site, through every handler.

```go
// cmd/api/config.go
// LogValue is what any slog call prints for a Config: which variables resolved, never their values.
func (c Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("port", c.Port),
		slog.Bool("database_url_set", c.DatabaseURL != ""),
		slog.Bool("jwt_secret_set", c.JWTSecret != ""),
		slog.Bool("smtp_url_set", c.SMTPURL != ""),
		slog.String("log_level", c.LogLevel),
		slog.String("log_format", c.LogFormat),
	)
}

var _ slog.LogValuer = Config{} // value receiver: Config and *Config both resolve through LogValue
```

`slog.Info("config loaded", "config", cfg)` then prints `"config":{"port":"8080","database_url_set":true,"jwt_secret_set":true,"smtp_url_set":false,"log_level":"info","log_format":"json"}`, and so does the accidental `slog.Info("boot", "config", cfg)` someone adds at three in the morning. The rule went from a warning to a property of the type.

- **Boolean for anything that can carry a credential, plain value only for what is printable by construction.** A DSN, a signing key, an SMTP URL become `_set` flags; a port, a level, a format print as they are, because an operator reading the boot line wants them.
- **Value receiver, and the assertion.** Verified: `cfg` (`*Config`), `*cfg` (`Config`) and `slog.Any("config", cfg)` all print the group. A pointer receiver would leave the plain `Config` value uncovered, and the struct would come back out with every field. `var _ slog.LogValuer = Config{}` is the `golang-patterns` idiom: the only place the link is written, and a compile error the day a rename breaks it.
- **The limit, stated.** `LogValue` intercepts slog argument resolution only. `fmt` verbs do not consult it, so `log.Printf("config: %+v", cfg)` still leaks, and that anti-pattern in `golang-bootstrap` stands as prose.
- **Same mechanism for any other type that can reach a log with a secret inside**: a token type whose `LogValue` returns `slog.StringValue("[redacted]")`, a user type that prints its id and never its email.
- **`HandlerOptions.ReplaceAttr` is the handler-level counterpart.** It sees every non-group attribute after resolution, by key, and can rename or drop it (rename `msg`/`time` for an ingestion pipeline, blank a key wholesale). It knows keys, not types, so it is the net under the `LogValuer`, never a substitute for it.

## The access log and the recovered panic

`SetDefault` bridges the `log` package's *default* logger only. chi's `middleware.Logger` does not use it: `middleware/logger.go` builds `DefaultLogger` on its own `log.New(os.Stdout, "", log.LstdFlags)` (line 177), so with `r.Use(middleware.Logger)` from `chi-patterns` the most frequent line the service writes is still a text line on stdout (colored only on a TTY: `terminal.go` gates the escape codes on `IsTTY`), outside slog, without a `request_id` key. And `middleware.Recoverer` routes a panic to `GetLogEntry(r).Panic(...)` when a logger middleware registered an entry, else to `PrintPrettyStack`, straight to stderr (`middleware/recoverer.go`, lines 32-36). Both are fixed by the same object, chi's own `RequestLogger(LogFormatter)` extension point, which `Logger` itself is built on.

```go
// internal/logging/access.go
// AccessLogger returns the middleware that replaces middleware.Logger in NewRouter:
// same position in the chain, one slog record per request instead of a text line.
func AccessLogger() func(http.Handler) http.Handler {
	return middleware.RequestLogger(accessFormatter{})
}

type accessFormatter struct{}

func (accessFormatter) NewLogEntry(r *http.Request) middleware.LogEntry {
	return accessEntry{r: r}
}

type accessEntry struct{ r *http.Request }

func (e accessEntry) Write(status, bytes int, _ http.Header, elapsed time.Duration, _ any) {
	slog.InfoContext(e.r.Context(), "request",
		"method", e.r.Method,
		"path", e.r.URL.Path,
		"status", status,
		"bytes", bytes,
		"duration_ms", float64(elapsed)/float64(time.Millisecond),
		"client_ip", middleware.GetClientIP(e.r.Context()),
	)
}

func (e accessEntry) Panic(v any, stack []byte) {
	slog.ErrorContext(e.r.Context(), "panic recovered", "err", v, "stack", string(stack))
}
```

In `NewRouter` (`chi-patterns`), `r.Use(middleware.Logger)` becomes `r.Use(logging.AccessLogger())`: same position, still above `Recoverer`, so the entry exists when `Recoverer` looks for it. Verified with a handler that panics: one ERROR record `panic recovered` with `err`, `stack` and `request_id`, then the INFO `request` record with `"status":500`, and the client receives the 500.

```json
{"time":"...","level":"INFO","msg":"request","method":"GET","path":"/","status":204,"bytes":0,"duration_ms":0.144,"client_ip":"192.0.2.1","request_id":"req-abc-123"}
```

- **`e.r` is the request as it was before `RequestLogger` attached its entry**, and its context already holds the id and the client IP because `RequestID` and `ClientIPFrom*` run above the logger in the chain. That is the ordering `chi-patterns` justifies; this is one more thing that depends on it.
- **`client_ip` is `""` unless exactly one `ClientIPFrom*` is installed**, the same trap `chi-patterns` documents for the rate limiter.
- **`duration_ms` as a float**, milliseconds with the sub-millisecond part. Passing the `time.Duration` itself renders as nanoseconds in JSON and as `1.2ms` in text: two shapes for one field.
- **`path` is `r.URL.Path`, never `RequestURI`, never the headers, never the body.** Bearer tokens travel in `Authorization` and, from careless clients, in query strings; neither belongs in a log.

## sloglint

`sloglint` (go-simpler/sloglint, actively maintained, v0.12.0 released April 2026, bundled in golangci-lint) enforces the shape everything above relies on. It goes into the `.golangci.yml` that `golang-patterns` owns: one more entry under `linters.enable`, one settings block. Do not copy that file here.

```yaml
linters:
  enable:
    - sloglint       # next to errorlint, bodyclose, noctx, ... from golang-patterns
  settings:
    sloglint:
      static-msg: true       # message is a literal or a constant; the variable part goes in an attribute
      key-naming-case: snake # request_id, client_ip, duration_ms: one convention, greppable
      context: scope         # slog.Info with a ctx in scope is an error: InfoContext, or the id is lost
```

- **`no-mixed-args` is on by default**: `slog.InfoContext(ctx, "m", "k", v, slog.String("k2", v2))` is flagged ("key-value pairs and attributes should not be mixed") without setting anything. Pick pairs or `slog.Attr` per call, and use pairs by default; they are what every example above uses.
- **What each rule catches, from its own diagnostic**: `"listening on "+addr` or `fmt.Sprintf(...)` as the message, "message should be a string literal or a constant"; `"listenAddr"` as a key, "keys should be written in snake_case"; `slog.Info(...)` in a function with a `ctx` in scope, "InfoContext should be used instead". A `ctx` declared later in the same function does not count as in scope, so `slog.Info("config loaded", ...)` right after `SetDefault` in `run()` is clean.
- **Spellings.** golangci-lint v2.13.1 validates the block and refuses `key-naming-case: snake_case` and `context: true` ("Options.KeyNamingCase has an invalid value", "Options.ContextOnly has an invalid value"). The values are `snake` and `scope` (or `all`); the linter's README is right, some rendered documentation pages are not.
- **Never `no-global`.** This design logs through the default logger on purpose: `SetDefault` once, `slog.InfoContext` everywhere, no logger threaded through constructors. `no-global` would flag every call in the service.
- `sloglint` also autofixes `slog.NewTextHandler(io.Discard, nil)` into `slog.DiscardHandler` (Go 1.24+), the handler a test passes when it builds a logger and wants silence. Tests never call `SetDefault`.

## Anti-Patterns

- **A third-party logger** (`zap`, `zerolog`, `logrus`) added for a speed nobody measured. `log/slog` is stdlib, the team is four people, and the handler is swappable if that day ever comes.
- **`log.Printf` / `fmt.Println` as logging anywhere below `main`.** No level, no structure, no `request_id`.
- **`slog.Info(...)` inside a handler, a service, or a repository with `ctx` in scope.** The id is lost; `context: scope` flags it.
- **`"request_id", middleware.GetReqID(r.Context())` typed by hand.** The handler does it; the manual copy is the one that is forgotten, or doubled under a second key.
- **A `*slog.Logger` stored in `context.Context`**, or a logger field on every struct. The stdlib rejected the first; the second threads a dependency the default logger already provides.
- **A wrapping handler that embeds `slog.Handler` and overrides only `Handle`.** `With` and `WithGroup` return the bare inner handler and the injection disappears without an error.
- **`slog.Info("boot", "config", cfg)` on a `Config` without `LogValue`**, or a `LogValue` on a pointer receiver.
- **The variable part interpolated into the message** (`"listening on " + addr`, `fmt.Sprintf`). Static message, attribute for the value; `static-msg` flags it.
- **`middleware.Logger` left in the chain.** Text on stdout, no JSON, no `request_id` key, and the panic stack goes wherever `PrintPrettyStack` sends it.
- **Headers, the raw query string, or the body in the access log.** Bearer tokens live there.
- **`no-global` in the `sloglint` settings.** It contradicts `SetDefault`.
- **`slog.SetDefault` in a test, a package `init`, or anywhere except `run()`.** One call, one place, right after `LoadConfig()`.
- **`LOG_FORMAT` defaulting to `text`.** The variable that gets missed is the production one.

## Related Skills

- `golang-bootstrap` for `run()`, `Config`, `LoadConfig` and its `errs` slice, the spot `slog.SetDefault` occupies (right after `LoadConfig()`, before the pool), and the `log.Fatalf` that stays in `main`.
- `chi-patterns` for `middleware.RequestID` at the head of the chain, `GetReqID`, the `ClientIPFrom*` choice the access log depends on, the position `AccessLogger` takes over from `middleware.Logger`, and `WriteAPIError`, whose manual `req_id` attribute this handler makes redundant.
- `golang-patterns` for the context rules (`ctx` first, never in a struct, values are request-scoped metadata only), the `.golangci.yml` the `sloglint` block goes into, the `_ =` rule on an ignored error, and the `var _ Interface = Type{}` assertion.
- `golang-websocket` for the drain step in shutdown whose `log.Printf` follows the same swap as the two in `golang-bootstrap`, and for the socket handlers whose lines carry the handshake request's id.
- `golang-testing` for `httptest` against a router that carries `AccessLogger`, and `slog.DiscardHandler` where a test builds its own logger.
- `version-standards` for Go 1.27.0, chi v5.3.2, and golangci-lint v2.13.1.
