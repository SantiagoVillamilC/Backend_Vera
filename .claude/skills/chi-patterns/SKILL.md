---
name: chi-patterns
description: Implementation patterns for Go HTTP APIs on go-chi/chi v5 over stdlib net/http. Covers router setup and grouping, the base middleware chain and its order, JSON binding with go-playground/validator, typed DTOs, the mandatory {success, data, error, meta} envelope, and request-scoped context values. Activate when designing or reviewing a chi endpoint, adding a route or middleware, or starting a Go HTTP service in this workspace. Chi is the Go counterpart of `hono-patterns`; use that one for the TypeScript services.
origin: ECC
---

# chi patterns

Conventions for Go HTTP services on `github.com/go-chi/chi/v5`. Paired with `api-design` (URL shape, status codes, pagination); this skill covers the how.

Baseline verified 2026-08-23: chi **v5.3.2**, module `github.com/go-chi/chi/v5`, minimum **Go 1.23**. No v6 and no v2 module exist. Chi has zero external dependencies: CORS, rate limiting and validation are separate modules by design.

## When to Activate

- Adding, moving or reviewing a route, a resource `Routes()` tree, or a `Mount` point.
- Writing or reordering middleware, especially anything touching client IP, CORS, rate limiting or auth.
- Binding and validating a request body, or shaping a response that must carry the envelope.
- Deciding what belongs in `context.Context` versus on a handler's dependency struct.
- Standing up a new Go HTTP service, or porting one off `net/http` mux or `gorilla/mux`.

## The response envelope (defined once, used everywhere)

Every response, success or failure, carries `{success, data, error, meta}`. All four keys always render; only `details` is omitted when empty.

```go
// internal/api/envelope.go
type Envelope[T any] struct {
	Success bool      `json:"success"`
	Data    T         `json:"data"`
	Error   *APIError `json:"error"`
	Meta    *Meta     `json:"meta"` // non-nil only on list endpoints
}
type APIError struct {
	Code    string       `json:"code"`    // stable and machine-readable: "ValidationError"
	Message string       `json:"message"` // human-readable, never a raw err.Error()
	Details []FieldError `json:"details,omitempty"`
}
type FieldError struct {
	Field string `json:"field"`
	Rule  string `json:"rule"`
	Param string `json:"param,omitempty"`
}
type Meta struct {
	Total int `json:"total"`
	Page  int `json:"page"`
	Limit int `json:"limit"`
}

func WriteData[T any](w http.ResponseWriter, status int, data T, meta *Meta) {
	writeJSON(w, status, Envelope[T]{Success: true, Data: data, Meta: meta})
}

func WriteError(w http.ResponseWriter, status int, code, msg string, details []FieldError) {
	writeJSON(w, status, Envelope[any]{Success: false, Error: &APIError{code, msg, details}})
}
```

`writeJSON` marshals into a buffer **first** (an encode failure must not arrive after `WriteHeader`, which would emit a truncated 200), then sets `Content-Type: application/json; charset=utf-8`, then `WriteHeader(status)`, then `Write(buf)`. Never write a bare object, and never `http.Error` with plain text from a handler.

## Router setup and grouping

One resource per struct, each exposing `Routes() chi.Router`, mounted from the top-level router. This is chi's own `todos-resource` idiom, and it is also where handler dependencies live.

```go
// The pgxpool is a process-wide singleton built at boot (`sqlc-patterns`). What the resource
// holds is the narrow generated interface over it, never the pool itself: that is the same
// "accept interfaces" seam that lets a test hand it a fake or a rolled-back pgx.Tx.
type UsersResource struct{ q dbstore.Querier }

func (rs UsersResource) Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/", rs.List)
	r.Post("/", rs.Create)
	r.With(paginate).Get("/search", rs.Search) // inline middleware, this endpoint only
	r.Route("/{userID}", func(r chi.Router) {  // sub-tree at a path prefix
		r.Use(rs.UserCtx) // loader runs for every verb below
		r.Get("/", rs.Get)
		r.Put("/", rs.Update)
		r.Delete("/", rs.Delete)
	})
	return r
}
```

The four organizers are not interchangeable:

| API | What it does | Use for |
|---|---|---|
| `r.Route(pattern, fn)` | inline sub-router at a path prefix | a resource sub-tree (`/{userID}/...`) |
| `r.Group(fn)` | inline sub-router at the **same** path with a **fresh** middleware stack | splitting public vs authenticated route sets |
| `r.Mount(pattern, h)` | attaches a separately built `http.Handler` at `pattern/*` | wiring a resource's `Routes()` into the tree |
| `r.With(mw...)` | returns a router with inline middleware | one endpoint that needs one extra middleware |

Params: `/users/{userID}` read with `chi.URLParam(r, "userID")`, wildcard `/admin/*` with `chi.URLParam(r, "*")`, regex constraints inline (`/articles/{rid:^[0-9]{5,6}}`).

## Middleware chain and composition order

```go
func NewRouter(users UsersResource, auth AuthResource) chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.ClientIPFromXFF("10.0.0.0/8")) // pick exactly ONE ClientIPFrom*
	r.Use(logging.AccessLogger()) // internal/logging (golang-logging): one slog record per request
	r.Use(middleware.Recoverer)
	r.Use(cors.Handler(cors.Options{ // go-chi/cors, top level only
		AllowedOrigins: []string{"https://app.example.com"},
		AllowedMethods: []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders: []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token"},
	}))
	r.Use(httprate.LimitBy(100, time.Minute, func(r *http.Request) (string, error) {
		return httprate.CanonicalizeIP(middleware.GetClientIP(r.Context())), nil
	}))

	r.Group(func(r chi.Router) { // public, fresh stack, same path space
		r.Use(middleware.Timeout(60 * time.Second))
		r.Post("/login", auth.Login)
		r.Get("/healthz", health)
	})
	r.Group(func(r chi.Router) { // authenticated
		r.Use(middleware.Timeout(60 * time.Second))
		r.Use(auth.RequireAuth)
		r.Mount("/users", users.Routes())
	})

	// chi's defaults answer plain text, which would break the envelope
	r.NotFound(notFoundHandler)
	r.MethodNotAllowed(methodNotAllowedHandler)
	return r
}
```

Why this order, top to bottom:

- **`RequestID` first**: every log line, error envelope and downstream middleware can then carry the id.
- **`ClientIPFrom*` before the logger and before the limiter.** This is the load-bearing one: with no `ClientIPFrom*` upstream, `middleware.GetClientIP` returns `""` and **every request shares a single rate-limit bucket**, silently tripping at 100 requests total. Pick one, there is no safe default: `ClientIPFromRemoteAddr` (server directly on the internet), `ClientIPFromHeader("CF-Connecting-IP")` (Cloudflare, or nginx `X-Real-IP`), `ClientIPFromXFF(cidrs...)` (preferred behind proxies whose CIDRs you can enumerate), `ClientIPFromXFFTrustedProxies(n)`.
- **`logging.AccessLogger()` above `Recoverer`, in the slot `middleware.Logger` used to hold.** It is chi's own `RequestLogger` with a slog formatter (`internal/logging/access.go`, defined in `golang-logging`): one slog record per request (JSON in production) through the handler installed by `slog.SetDefault`, with `request_id` and `client_ip` read from the context the entry captured at this position, which is why `RequestID` and `ClientIPFrom*` stay above it. `Recoverer` looks that entry up with `GetLogEntry(r)` and sends a panic through its `Panic` method, so the stack lands as an ERROR record for that request and the access line still follows with `"status":500`. `middleware.Logger` never goes back in: it writes through its own `log.New(os.Stdout, ...)` (`middleware/logger.go:177`), outside `slog.SetDefault`, so in production the most frequent line the service emits would be plain text on stdout without a `request_id`. Verified against chi v5.3.2 with this exact chain: stdout stays empty, every request yields one `request` record with the id from `X-Request-Id` (or the one chi generated), and a 5xx through `WriteAPIError` yields one ERROR record carrying that same id under a single key.
- **CORS must be a top-level `r.Use`.** Inside `r.Group()` or `With()` it will not work: the preflight `OPTIONS` has to reach the middleware before routing narrows.
- **Rate limit after CORS** so a `429` still carries CORS headers, and after `ClientIPFrom*` per the point above. `CanonicalizeIP` buckets IPv6 by /64, so a client cannot rotate SLAAC addresses for a fresh bucket.
- **`Timeout` inside each REST group, never on the root.** A root `r.Use` wraps the whole tree (`mux.go`, `updateRouteHandler`: the root handler is `chain(mx.middlewares, routeHTTP)`), `Group`s and `Mount`ed sub-routers included, and a group can only add to that stack, never subtract from it. A route that must not carry a deadline (a WebSocket upgrade, see `golang-websocket`) can therefore only be exempted if `Timeout` lives in the groups that want it: both REST groups declare it as their first `Use`, a socket group declares none. The deadline still covers what it covered at the bottom of the root chain, the handler and `RequireAuth` rather than the logging and recovery around them. Verified against chi v5.3.2: every route of both groups, the `Mount`ed `/users` sub-tree included, sees the deadline on `r.Context()` and answers 504 past it (for a handler that returns on `ctx.Done()` without writing, the only case the middleware emits the 504); a third group without that `Use` sees no deadline at all, while the same `Timeout` placed on the root reaches it.
- **Auth is never global.** It goes inside the authenticated `r.Group()`, so `/login` and `/healthz` stay reachable.

`r.Use(...)` must be called before any route is registered on that router; chi panics otherwise. Custom middleware lives in `internal/middleware/`, one file per concern, unless it serves a single resource, in which case it stays a method on that resource (see `UserCtx` below).

## Request binding and validation

There is no `zValidator` analogue in Go: no validation middleware slot exists, decode plus validate happens in the first lines of the handler. `go-chi/render` is dormant (last commit 2023-10) and must not anchor new code. Use stdlib `encoding/json` plus `go-playground/validator/v10`.

```go
// ONE instance per process: it caches struct metadata and is concurrency-safe.
var validate = validator.New(validator.WithRequiredStructEnabled())

type createUserRequest struct {
	Email string `json:"email" validate:"required,email"`
	Name  string `json:"name"  validate:"required,min=1,max=200"`
	Age   int    `json:"age"   validate:"omitempty,gte=13,lte=120"`
}

// returns false once a 4xx/5xx envelope has already been written.
func decodeAndValidate(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields() // client typos rejected, not silently dropped
	if err := dec.Decode(dst); err != nil {
		var big *http.MaxBytesError
		if errors.As(err, &big) {
			WriteError(w, 413, "PayloadTooLarge", "Request body exceeds 1 MiB.", nil)
		} else { // *json.SyntaxError, *json.UnmarshalTypeError and io.EOF all land here
			WriteError(w, 400, "BadRequest", "Request body is not valid JSON.", nil)
		}
		return false
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		WriteError(w, 400, "BadRequest", "Body must contain a single JSON object.", nil)
		return false
	}
	var ve validator.ValidationErrors
	if err := validate.Struct(dst); errors.As(err, &ve) {
		details := make([]FieldError, 0, len(ve))
		for _, fe := range ve {
			details = append(details, FieldError{fe.Field(), fe.Tag(), fe.Param()})
		}
		WriteError(w, 422, "ValidationError", "Request validation failed.", details)
		return false
	} else if err != nil {
		WriteError(w, 500, "InternalError", "Something went wrong.", nil)
		return false
	}
	return true
}
```

- **400 vs 422**: malformed JSON is `400`, well-formed JSON that fails a rule is `422` with per-field `details`. Same split as the Hono side.
- One request struct per endpoint, declared next to its handler; rules as tags, not imperative checks.
- Never call `validator.New()` inside a handler: it throws away the metadata cache on every request.

## Typed handlers and DTOs

**Go has no equivalent of Hono's chained route type inference.** There is no `hc<AppType>` client and no compile-time link between a path and a handler's request or response type: registering a handler under the wrong path or method still compiles. Signatures stay `func(http.ResponseWriter, *http.Request)`. What you get instead is typing at the two ends, a typed struct on the way in and `WriteData[T]` on the way out.

```go
func (rs UsersResource) Create(w http.ResponseWriter, r *http.Request) {
	var in createUserRequest
	if !decodeAndValidate(w, r, &in) {
		return // envelope already written
	}
	user, err := rs.create(r.Context(), in) // returns userResponse, a separate type
	if err != nil {
		WriteAPIError(w, r, err)
		return
	}
	WriteData(w, http.StatusCreated, user, nil) // 201 explicitly, not the 200 default
}

func (rs UsersResource) List(w http.ResponseWriter, r *http.Request) {
	page, limit := paginationFrom(r.URL.Query()) // the server caps limit, not the client
	users, total, err := rs.list(r.Context(), page, limit)
	if err != nil {
		WriteAPIError(w, r, err)
		return
	}
	// the only place meta is non-nil
	WriteData(w, http.StatusOK, users, &Meta{Total: total, Page: page, Limit: limit})
}
```

A generic adapter removes that boilerplate where the shape fits, `func Handle[In, Out any](status int, fn func(context.Context, In) (Out, error)) http.HandlerFunc`: it decodes into `In`, calls `fn`, and writes `WriteData(status, out)` or `WriteAPIError`, so registration becomes `r.Post("/", Handle(http.StatusCreated, rs.createUser))`. It hides `http.ResponseWriter`, so anything that streams, sets headers, or reads URL params keeps the full signature.

DTO rules: request and response are separate types, never bind a request straight onto a DB row struct (mass assignment), and use pointer fields on PATCH bodies so "absent" is distinguishable from the zero value.

## Centralized error handling

Go handlers return nothing, so there is no `app.onError` hook a middleware can catch. Centralization comes from one error type plus one writer called at every handler exit.

```go
type Error struct {
	Status  int
	Code    string
	Message string
	Err     error // wrapped cause: logged, never serialized
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }
func (e *Error) Unwrap() error { return e.Err }

var ErrUserNotFound = &Error{404, "UserNotFound", "User not found.", nil}
var ErrEmailTaken = &Error{409, "EmailTaken", "That email is already registered.", nil}

func WriteAPIError(w http.ResponseWriter, r *http.Request, err error) {
	var apiErr *Error
	if errors.As(err, &apiErr) && apiErr.Status < 500 {
		WriteError(w, apiErr.Status, apiErr.Code, apiErr.Message, nil)
		return
	}
	slog.ErrorContext(r.Context(), "unhandled request error", "err", err,
		"method", r.Method, "path", r.URL.Path) // request_id is stamped by the handler in golang-logging
	WriteError(w, 500, "InternalError", "Something went wrong.", nil)
}
```

- **Never serialize `err.Error()` on a 5xx.** It can carry SQL text, file paths, credentials. Log the cause, return a fixed message plus the request id.
- Domain layers return `fmt.Errorf("create user: %w", ErrEmailTaken)`; only the HTTP layer knows status codes.
- `middleware.Recoverer` answers a panic with a bare 500 and no body, which is outside the envelope. If the envelope must hold for every response, wrap it or write a recoverer that calls `WriteError`.

## Request-scoped values in context.Context

**A DB pool is not request-scoped and does not belong in `context.Context`.** Neither does the querier built over it. The dependency goes on the resource struct (`UsersResource.q` above), wired once at construction and compile-time checked; anything pulled out of context is a runtime type assertion that can panic, and it silently makes every handler untestable without a full context fixture. See `sqlc-patterns` for pool construction, the `Querier` interface, queries and transactions.

Context carries per-request values only: request id, authenticated user or claims, trace span, resolved client IP, tenant id, and an entity resolved once by a loader middleware.

```go
type ctxKey int // unexported type, never a bare string (staticcheck SA1029)

const userKey ctxKey = iota

// Export typed accessors, never the key itself.
func WithUser(ctx context.Context, u *User) context.Context {
	return context.WithValue(ctx, userKey, u)
}
func UserFrom(ctx context.Context) (*User, bool) { u, ok := ctx.Value(userKey).(*User); return u, ok }

// Loader middleware as a method: it reaches the querier through the struct,
// and only the resolved entity goes into the context.
func (rs UsersResource) UserCtx(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, err := rs.getUser(r.Context(), chi.URLParam(r, "userID"))
		if err != nil {
			WriteAPIError(w, r, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(WithUser(r.Context(), u)))
	})
}
```

Request id: `r.Use(middleware.RequestID)`, then `middleware.GetReqID(r.Context())` anywhere downstream. The header name is the package var `middleware.RequestIDHeader` (default `X-Request-Id`), reassignable at startup. For a per-route **constant** (tenant, feature flag) `middleware.WithValue(key, val)` is enough; it cannot express a derived per-request value, since it captures once at build time.

## Testing a handler with httptest

Exercise the router, not the bare handler: through `r.ServeHTTP` the middleware chain runs and `chi.URLParam` resolves.

```go
func TestCreateUser_ValidationEnvelope(t *testing.T) {
	r := chi.NewRouter()
	// withTx returns *dbstore.Queries on a transaction rolled back in t.Cleanup, and it satisfies
	// dbstore.Querier, so the resource takes it unchanged. See `golang-testing` §5, Tier 1.
	r.Mount("/users", UsersResource{q: withTx(t)}.Routes())
	req := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"email":"nope","name":""}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	var got Envelope[json.RawMessage]
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Success || got.Error == nil || got.Error.Code != "ValidationError" {
		t.Fatalf("envelope = %+v", got)
	}
}
```

To test a handler in isolation and still resolve URL params, seed a route context: `rctx := chi.NewRouteContext(); rctx.URLParams.Add("userID", "42")`, then `req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))`. Table-driven cases, DB fixtures and coverage live in `golang-testing`.

## Anti-Patterns

- **`middleware.RealIP`.** Deprecated in chi v5.3.0 over three spoofing advisories (GHSA-3fxj-6jh8-hvhx, Critical 9.3): it mutated `r.RemoteAddr` from client-supplied headers with no proxy-trust check. The deprecation is a doc comment only, so it still compiles and still ships the vulnerability, and chi's own `_examples/todos-resource` has not been updated.
- **`httprate.LimitByIP` / `LimitByRealIP` / `KeyByRealIP` / `WithKeyByIP`.** Deprecated in httprate v0.16.0 for the same reason: an attacker rotates the key to evade the limit, or pins it to a victim's IP to lock that victim out with 429s. Use `LimitBy` with an explicit `KeyFunc`. The chi docs site still shows `LimitByIP` with v1 import paths; do not copy that snippet.
- **Raw string context keys.** `context.WithValue(ctx, "user", u)` collides across packages and is flagged by SA1029. Chi's README and `_examples/rest` still do it: that is illustrative shorthand, not the pattern.
- **DB pool in the context, or the raw `*pgxpool.Pool` as the resource field.** A `dbstore.Querier` on the dependency struct, always: a resource typed against the concrete pool cannot be handed a `pgx.Tx`, which is exactly what `golang-testing`'s transaction-rollback tier needs.
- **`json.NewDecoder(r.Body).Decode(&in)` with nothing else.** No `MaxBytesReader` is an unbounded-body DoS; no `DisallowUnknownFields` silently swallows client typos.
- **`validator.New()` per request**, or a second validator instance per package.
- **`http.Error(w, "not found", 404)` inside a handler.** Plain text breaks the envelope; route everything through `WriteError` / `WriteAPIError`.
- **`r.Use(...)` after routes are registered on that router.** Chi panics at startup.
- **CORS applied inside `r.Group()` or `With()`.** Preflight silently fails.
- **`go-chi/render` for new binding code.** Dormant since 2023-10, no releases.
- **Handlers over 50 lines.** Extract to a service method on the resource struct.

## Related Skills

- `sqlc-patterns`: pool construction, the `Querier`/`DBTX` interfaces these resources hold, queries and transactions, and the SQLSTATE mapping that feeds `WriteAPIError`.
- `golang-patterns`: general Go idioms, error wrapping, interfaces, project layout.
- `golang-testing`: the full testing treatment, table-driven tests, the `withTx` fixture used above, coverage.
- `api-design`: URL shape, status codes, pagination, the envelope contract itself.
- `hono-patterns`: the TypeScript counterpart, the same concerns on Hono.
