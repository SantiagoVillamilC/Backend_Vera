---
name: golang-websocket
description: Real-time push for a Go service on this stack (chi v5 + pgx/sqlc against self-hosted PostgreSQL, HS256 JWT verified in `internal/auth`) with `github.com/coder/websocket`, the maintained successor of `nhooyr.io/websocket`, chosen over the dormant `gorilla/websocket`. Covers the domain rule that a socket carries signals and never state (the client re-reads by REST, so a slow consumer can always be dropped), mounting the upgrade route in a chi group that does not inherit `middleware.Timeout`, authenticating the handshake with the same `Authorization: Bearer` middleware as the REST API (and the one-time-ticket pattern if a browser client ever appears), `Accept` with explicit `OriginPatterns`, the dedicated reader (`CloseRead`), keepalive with `Ping` under a budget, the non-blocking outbound buffer, the single close path per connection with the close codes the mobile client switches on, and the connection registry plus `sync.WaitGroup` that graceful shutdown must drain before river stops, because `srv.Shutdown` waits for neither hijacked connections nor `RegisterOnShutdown` callbacks. Activate when adding or reviewing a WebSocket endpoint, a push to the mobile app, a connection registry or hub, a ping/pong or keepalive loop, an `Accept` call or `OriginPatterns`, or the shutdown path of a service that holds sockets. Not for routing, the middleware chain, or the envelope (see `chi-patterns`), not for the rest of boot and shutdown, config, or river (see `golang-bootstrap`), not for goroutines, channels, or context in general (see `golang-patterns`), not for queries or the pool (see `sqlc-patterns`), and not for tests beyond the one `Dial` example (see `golang-testing`).
---

# Go WebSocket

The one long-lived connection in a service where everything else is request and response. A socket here is a doorbell, not a ledger: it tells the mobile app that something it holds is stale, and the app re-reads it through the REST API. Every rule below (drop slow consumers, never guarantee delivery, close on shutdown without ceremony) is safe only because of that.

Library: `github.com/coder/websocket` **v1.8.15** (formerly `nhooyr.io/websocket`; that import path is deprecated). It requires Go 1.23+, satisfied by the **1.27.0** toolchain in `version-standards`; pin the exact version in `go.mod`, never `@latest`. Verified 2026-09-04 against the v1.8.15 source, the chi v5.3.2 source, and `net/http` on go1.27.0. The connection, registry, handler, and shutdown blocks below compiled, vetted, and ran under `-race` against those versions (the eight tests are listed in the testing section); the router and test snippets are abridged to what they demonstrate.

## Role and boundaries (which skill to use)

This skill = the upgrade handler, the per-connection loops, the backpressure contract, the origin check, and the registry that plugs the socket drain into the existing shutdown sequence. Everything around it already has an owner:

- **Routing, the middleware chain and its order, `RequireAuth`, the `{success, data, error, meta}` envelope** → `chi-patterns`. This skill only says where the upgrade route sits and why `middleware.Timeout` cannot sit above it.
- **The composition root, `LoadConfig`, `/healthz` and `/readyz`, river, and the shutdown sequence** → `golang-bootstrap`. This skill adds one step to that sequence and a handful of lines to `run()`; it does not restate the rest.
- **Goroutines, channels, `select`, `sync.WaitGroup`, `sync.Once`, context rules, goroutine leaks** → `golang-patterns`.
- **Token verification** (HS256 with the shared `JWT_SECRET`) → the service's own `internal/auth` (`ValidateJWT` in `internal/auth/service.go`). The socket reuses its middleware; it verifies nothing itself.
- **Queries, transactions, the pool** → `sqlc-patterns`. A socket handler holds the registry, not a querier.
- **Out of scope, on purpose**: client-to-server frames (the protocol is strictly server to client; the client talks through REST, and `CloseRead` enforces it), and fan-out across replicas. With one replica the in-process registry is the whole delivery path; with more, the signal has to cross processes first (Postgres `LISTEN/NOTIFY` on the same pool, or Redis pub/sub per `redis-patterns`) and it stays a signal.

## When to Activate

- Adding or reviewing a route that calls `websocket.Accept`, or any file importing `github.com/coder/websocket`.
- Pushing anything to the mobile app: a balance change, a transfer state, a notification badge.
- Writing a hub, broker, registry, or "subscribers" map; a ping/pong or keepalive loop; a per-connection goroutine.
- Anything touching `AcceptOptions`, `OriginPatterns`, `InsecureSkipVerify`, or a `CheckOrigin` copied from a gorilla tutorial.
- Touching `srv.Shutdown`, `RegisterOnShutdown`, or the shutdown order in a service that holds sockets.
- Someone proposing `gorilla/websocket`, `nhooyr.io/websocket`, or `golang.org/x/net/websocket`.

## Library: coder/websocket, not gorilla

`github.com/coder/websocket` is the choice, and it is not a coin flip:

- **Maintenance.** gorilla/websocket has had no release since June 2024 and no push since March 2025, under maintainers who stated in writing that Red Hat is not taking over the project. coder/websocket released and pushed in June 2026.
- **`context.Context` on every blocking call.** `Accept`, `Read`, `Write`, `Ping`, `Dial` all take a context, so the deadline rules in `golang-patterns` apply unchanged. gorilla exposes `SetReadDeadline` / `SetWriteDeadline` on the connection instead.
- **Concurrent writes are safe.** gorilla's own doc supports one concurrent reader and one concurrent writer, which is why every gorilla example runs a writer goroutine fed by a channel just to serialize writes. coder/websocket serializes internally ("all methods may be called concurrently except for Reader and Read"). The per-connection write goroutine below still exists, but for backpressure and ordering, not for safety.
- **Still one reader, and it must never stop.** `Read` / `Reader` are the exception to the concurrency rule, and control frames (ping, pong, close) are only handled while something is reading. This skill uses `CloseRead`, which starts that reader for you and rejects data frames.
- **Two caveats to know before they bite.** On a failed write, or a context that ends during one, the library closes the socket itself, without a close frame. On a pong that never arrives it does the opposite: `Ping` returns an error and leaves the socket open (`conn.go`, `ping`: the wait on `ctx.Done` returns without closing), whatever the `Conn` doc's "any error closes the connection" suggests. Both shape the lifecycle section: contexts for writes are short and separate, and the keepalive loop closes on its own.

`nhooyr.io/websocket` is the same code under its old import path and is formally deprecated. `golang.org/x/net/websocket` has no context support, and its own package doc sends readers to other packages as more actively maintained (its list still names gorilla, so weigh the maintenance bullet above before following it). Neither is an option.

## Mounting the upgrade route

```go
// internal/api/router.go, the NewRouter from chi-patterns plus the socket group
func NewRouter(users UsersResource, auth AuthResource, health HealthResource, events EventsResource) chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.ClientIPFromXFF("10.0.0.0/8"))
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	// cors.Handler and httprate.LimitBy exactly as in chi-patterns.
	// No middleware.Timeout at the root, as in chi-patterns: a root r.Use wraps every route
	// in the tree, groups included, so each REST group declares its own below.

	r.Group(func(r chi.Router) { // public REST
		r.Use(middleware.Timeout(60 * time.Second))
		r.Post("/login", auth.Login)
		r.Get("/healthz", health.Live)
		r.Get("/readyz", health.Ready)
	})
	r.Group(func(r chi.Router) { // authenticated REST
		r.Use(middleware.Timeout(60 * time.Second))
		r.Use(auth.RequireAuth)
		r.Mount("/users", users.Routes())
	})
	r.Group(func(r chi.Router) { // WebSocket: same auth, NO Timeout; the deadlines live in the handler
		r.Use(auth.RequireAuth)
		r.Mount("/ws", events.Routes())
	})

	r.NotFound(notFoundHandler)
	r.MethodNotAllowed(methodNotAllowedHandler)
	return r
}
```

- **`Group` cannot subtract a root middleware.** chi's `Group` gives a fresh stack for what the group *adds*; whatever the root registered with `r.Use` wraps the whole tree (`mux.go`: the root handler is `chain(mx.middlewares, http.HandlerFunc(mx.routeHTTP))`, and an inline group only prepends its own middlewares to each route it registers). That is why `chi-patterns` declares `middleware.Timeout(60 * time.Second)` inside each REST group instead of at the root: a group that wants the deadline adds it, and the socket group adds none. Nothing in that router changes for this skill; it only gains the third group.
- **What `Timeout` does to a socket.** It puts a 60s deadline on `r.Context()` and, when the handler finally returns, writes a 504. The first half is the trap: anything derived from the request context after the upgrade (a membership check when a signal arrives, a log call with context, a future read loop) dies at the 60s mark, which is why the lifecycle section derives nothing from `r.Context()` after `Accept`. The second half is silent under this chain: chi's `Logger` wrapper already recorded `Accept`'s 101 and swallows the late 504 (`wrap_writer.go`, `basicWriter.WriteHeader`), so the access log shows a 101 and nothing reaches `net/http` (a bare `net/http` writer would log `http: response.WriteHeader on hijacked connection`). Verified with a 100ms `Timeout` placed above the socket group on purpose: the connection outlived it and the log said 101. A deadline a session must not inherit plus a header write that goes nowhere: neither belongs above the socket, and the group without `Timeout` makes that visible.
- **The `http.Server` timeouts stop at the upgrade.** `Hijack` clears the connection's deadlines (`server.go`, `hijackLocked`: `rwc.SetDeadline(time.Time{})`), so `ReadTimeout`, `WriteTimeout`, and `IdleTimeout` from `golang-bootstrap` no longer apply. The only deadlines the socket has are the ones the lifecycle section sets.
- **The rest of the root chain is fine.** `RequestID`, `ClientIPFrom*`, `Logger`, `Recoverer`, CORS, and the rate limiter all run once, at the handshake, which is an ordinary GET. `Logger` wraps the writer and that is harmless: chi's wrappers forward `Hijack`, and `Accept` also follows `Unwrap()` the way `http.ResponseController` does. Expect one access-log line per socket session, written at disconnect, with the session length as its elapsed time; that is `Logger` working, not a bug to fix by removing it.
- The rate limiter buckets handshakes per client IP like any other request, which is what you want: a reconnect storm from one device is a handshake storm.

## Authenticating the handshake

The client is a mobile app, so the upgrade request is an ordinary authenticated GET: `Authorization: Bearer <token>`, verified by the same `RequireAuth` that guards `/users`, mounted in the socket group above. No token in the query string, no subprotocol trick. The handler reads the claims **before** `Accept`, because after the upgrade there is no HTTP response left to write.

Two facts about `Accept` decide the order of operations:

- `Accept` answers every handshake failure itself with `http.Error`: plain text, outside the `chi-patterns` envelope. That is acceptable for a protocol-level failure (a client missing `Upgrade`, a bad origin), and it is the reason authentication runs in the middleware, before the handler, where a 401 still carries the envelope.
- `Accept` needs an `http.Hijacker`. `httptest.NewRecorder` is not one (a well-formed handshake against it gets a 501, a malformed one a 400 or 426 first; no upgrade either way), so handler tests use `httptest.NewServer` (see the testing section).

The token's `exp` bounds the session. A connection accepted at 09:00 with a token that expires at 09:15 must not still be open at 10:00: the lifecycle below derives the connection's context from a `context.WithDeadline` at `exp`, and closes with an application code the client understands as "refresh, then reconnect". For that, `RequireAuth` has to put the expiry in the context next to the user id. Today `internal/auth.ValidateJWT` returns `User{ID, Email}` and nothing else; exposing `exp` is a change to `internal/auth`, made once, before the deadline is wired.

**If a browser client ever appears.** The browser `WebSocket` constructor cannot set an `Authorization` header. The clean pattern is a one-time ticket: the page calls an authenticated REST endpoint (`POST /ws/tickets`), receives a random opaque token with a lifetime of tens of seconds, and opens the socket with `?ticket=...`; the socket handler validates and consumes the ticket before `Accept`, so a replay fails. Do not smuggle the JWT through `Sec-WebSocket-Protocol`: it abuses a negotiation header for a credential and leaks into every proxy log that records the handshake. A ticket in the query string reaches the proxy access log too, which is why it is single-use and short-lived; storing consumed tickets across replicas is the multi-replica problem this skill leaves out of scope.

## Connection lifecycle

One handler, one dedicated reader, one writer, one keepalive goroutine, one close.

```go
// internal/api/events.go
type EventsResource struct {
	reg     *ws.Registry
	origins []string // browser origins allowed to connect; empty for the mobile-only case
}

func NewEventsResource(reg *ws.Registry, origins []string) EventsResource {
	return EventsResource{reg: reg, origins: origins}
}

func (rs EventsResource) Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/", rs.Connect)
	return r
}

const pingEvery = 30 * time.Second

func (rs EventsResource) Connect(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.ClaimsFrom(r.Context()) // put there by RequireAuth: read it BEFORE Accept
	if !ok {
		WriteError(w, http.StatusUnauthorized, "Unauthorized", "Authentication required.", nil)
		return
	}
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: rs.origins})
	if err != nil {
		return // Accept already answered with http.Error: plain text, outside the envelope
	}

	// Not r.Context(): after the upgrade it is no longer a reliable signal (Accept's doc),
	// and it would carry a middleware.Timeout deadline if the route inherited one.
	readCtx := c.CloseRead(context.Background())                   // done once the socket is closed
	ctx, cancel := context.WithDeadline(readCtx, claims.ExpiresAt) // the session never outlives the token
	defer cancel()

	conn := ws.NewConn(claims.UserID, c, cancel)
	if !rs.reg.Add(conn) { // shutdown began between the handshake and the registration
		_ = c.Close(websocket.StatusGoingAway, "server shutting down")
		return
	}
	defer func() {
		_ = c.Close(conn.CloseReason()) // the ONE Close; nil or net.ErrClosed if the socket already died
		rs.reg.Remove(conn)             // wg.Done() inside, after the socket is closed: shutdown waits for this
	}()

	go conn.Keepalive(ctx, pingEvery)
	conn.WriteLoop(ctx) // returns when the peer leaves, a Drop lands, or the token expires
}
```

The connection type and its two loops:

```go
// internal/ws/conn.go
const (
	writeTimeout = 5 * time.Second
	pongTimeout  = 5 * time.Second
)

// Close codes the mobile client switches on. 4000-4999 is the application range of RFC 6455.
const StatusTokenExpired websocket.StatusCode = 4001 // refresh the token, then reconnect

type Conn struct {
	UserID uuid.UUID
	ws     *websocket.Conn
	out    chan Signal
	cancel context.CancelFunc
	once   sync.Once
	code   websocket.StatusCode
	reason string
}

func NewConn(userID uuid.UUID, ws *websocket.Conn, cancel context.CancelFunc) *Conn {
	return &Conn{UserID: userID, ws: ws, out: make(chan Signal, 16), cancel: cancel}
}

// Drop records the first close reason and stops the loops. It never touches the socket:
// the handler performs the one and only Close (a second Close parks for up to 15s and
// returns net.ErrClosed), so every path that wants the connection gone calls Drop.
func (c *Conn) Drop(code websocket.StatusCode, reason string) {
	c.once.Do(func() {
		c.code, c.reason = code, reason
		c.cancel()
	})
}

// CloseReason is what the handler sends: NormalClosure unless a Drop came first.
func (c *Conn) CloseReason() (websocket.StatusCode, string) {
	c.once.Do(func() { c.code, c.reason = websocket.StatusNormalClosure, "" })
	return c.code, c.reason
}

// WriteLoop is the only writer of data frames on this connection. It returns when ctx ends
// (peer gone, a Drop, the token deadline) or when a write fails, in which case the library
// has already closed the socket.
func (c *Conn) WriteLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) { // only the token deadline produces this
				c.Drop(StatusTokenExpired, "token expired")
			}
			return
		case s := <-c.out:
			// Background, not ctx: a context that ends during a write makes the library close
			// the socket with no close frame (context.AfterFunc in conn.go), which is the
			// abrupt close every other path here avoids.
			wctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
			err := wsjson.Write(wctx, c.ws, s)
			cancel()
			if err != nil {
				return
			}
		}
	}
}

// Keepalive is the read deadline of this design: Ping waits for the pong, so no pong
// inside the budget means the peer is gone even if the TCP socket has not noticed.
// A pong timeout is the one error the library does NOT close the socket on (conn.go,
// ping: the select on ctx.Done returns without close), so this loop has to Drop.
func (c *Conn) Keepalive(ctx context.Context, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pctx, cancel := context.WithTimeout(context.Background(), pongTimeout)
			err := c.ws.Ping(pctx)
			cancel()
			if err != nil {
				c.Drop(websocket.StatusPolicyViolation, "no pong") // a no-op if the socket already died
				return
			}
		}
	}
}
```

- **`CloseRead` is the dedicated reader.** The protocol is server to client only, so the handler never reads a data frame; `CloseRead` starts the goroutine that services ping, pong, and close frames, closes the connection with `StatusPolicyViolation` if the client sends data anyway, and returns a context that is cancelled when the socket closes. That context is the root of everything else: when the peer disappears, every loop above ends through it. If a future feature needs client-to-server frames, this becomes an explicit `for { c.Read(ctx) }` goroutine with `SetReadLimit`, still exactly one reader.
- **Three contexts, three jobs.** `readCtx` (from `CloseRead`, rooted in `Background`) says the socket is gone. `ctx` (its child with the token deadline, and the `cancel` that `Drop` calls) drives the two `select` loops. Per-write and per-ping contexts are `Background` plus a short timeout, never `ctx`: the library arms a `context.AfterFunc` that closes the socket outright when a context handed to a write ends, so a session deadline reaching a write in flight would skip the close frame the client relies on.
- **One `Close` per connection, in the handler's `defer`.** `Close` is a handshake: it writes the close frame (5s budget) and waits up to 5s for the peer's frame before cutting the socket. A client that is reading (every real client is, that is how it receives signals) answers within a round trip, under 1ms on loopback; a dead one costs the full 5s. A second `Close` from anywhere else is not free: it parks on `waitGoroutines` for up to 15s and returns `net.ErrClosed`. So slow-consumer drops, a missing pong, token expiry, and shutdown all go through `Drop`, which records a reason and cancels, and the handler sends the frame. That final `Close` returns nil when the library had already cut the socket (a failed write) and `net.ErrClosed` when the peer's close frame or `CloseRead`'s policy close got there first; neither is a failure to log.
- **Keepalive is the read deadline.** gorilla's idiom is a pong handler that extends `SetReadDeadline`; here `Ping` blocks until the pong arrives or its context ends, so "no pong within `pongTimeout`" *is* the deadline. The library does **not** close the socket on that timeout (it does on a failed write, not on a missing pong), so the loop calls `Drop` and the handler's `Close` finishes the job. Against a peer that never answers, that `Close` spends its 5s wait for the peer's frame before cutting the socket, so a dead peer costs `pongTimeout` plus 5s per connection: measured at 10.2s with a 200ms ping interval. At shutdown those waits run in parallel across handlers and stay inside the 30s budget. The loop runs in its own goroutine because a dead peer stalls `Ping` for the whole budget and the writer should not stall with it; concurrent writes are safe. The library's doc notes that TCP keepalives usually suffice; they do not for this client: a phone behind carrier NAT or an nginx `proxy_read_timeout` (60s by default) drops an idle socket silently, and the kernel's keepalive default is two hours. `pingEvery` stays below the proxy's read timeout.
- **Close codes are the client's protocol.** `StatusNormalClosure` (1000): nothing to do. `StatusGoingAway` (1001): the server is restarting, back off and reconnect. `StatusPolicyViolation` (1008) with "slow consumer" or "no pong": reconnect and re-read by REST. `StatusTokenExpired` (4001): refresh the token first. A dropped TCP connection with no frame (a failed write or ping) is "reconnect with backoff" like 1001.
- A ping or write failure is ordinary on mobile (tunnels, elevators, app backgrounding) and worth a debug line at most, never an error log.

## Backpressure: signals, never state

```go
// internal/ws/signal.go
// A Signal names what changed and where to re-read it. It never carries the new value:
// the client answers a signal with a REST call, which is authoritative and idempotent.
type Signal struct {
	Kind       string    `json:"kind"`        // "account.updated", "transfer.settled"
	ResourceID string    `json:"resource_id"` // what to re-fetch by REST
	At         time.Time `json:"at"`
}

// Send never blocks the caller. A full buffer means this client is not draining fast
// enough; dropping it is safe because it re-reads by REST on reconnect.
func (c *Conn) Send(s Signal) {
	select {
	case c.out <- s:
	default:
		c.Drop(websocket.StatusPolicyViolation, "slow consumer")
	}
}
```

Why this is decidable for a wallet, and not a trade-off to revisit per feature:

- **A balance over the socket would need exactly-once delivery, and WebSocket does not have it.** A frame written just before a reconnect is gone; two frames can arrive after the state they describe has changed again; a buffered frame can land minutes late on a flaky link. Money displayed from such a stream is money displayed wrong. The REST read is idempotent and authoritative (the row, per `sqlc-patterns`), so the socket carries *that something changed*, and the client asks REST *what it is now*.
- **Under that rule, dropping a connection never loses information.** The worst case is a client that reconnects and does one extra REST read. So the publisher never blocks, never queues without bound, never retries: a full 16-slot buffer means the consumer is not keeping up, and the cure is to cut it loose.
- **Two valves, not one.** The buffer catches bursts; the 5s `writeTimeout` catches a stalled TCP window (a phone that stopped reading with its receive buffer full). Both end in a close, and both are correct.
- **Coalescing is free.** Ten `account.updated` for the same account inside one second are one REST read on the client. Push all ten, or dedupe by `(Kind, ResourceID)` in the publisher; never batch state into the signal to "save" a REST call.
- **Publish after the commit, never inside the transaction.** The publisher is a service method calling `registry.Notify(userID, Signal{...})` once `tx.Commit` returned. Sent inside the transaction, the signal can reach the phone before the commit is visible and make it read stale data; sent after a rollback it costs one wasted read. The first is a correctness bug, the second is noise.

## Connection registry and graceful shutdown

`srv.Shutdown` (`golang-bootstrap`, step 1) closes listeners, closes idle connections, and waits for active ones to become idle. Two things it does **not** do, both verified in `net/http/server.go`: it does not wait for hijacked connections (they leave the server's `activeConn` set at hijack time), and it does not wait for the callbacks registered with `RegisterOnShutdown`, which it starts with `go f()` and forgets; those callbacks are documented as "should not wait for shutdown to complete". Neither coder/websocket nor gorilla ships a registry to bridge that gap (coder/websocket tracks "graceful shutdown helpers" as an open roadmap item, issue #209). The registry is application code:

```go
// internal/ws/registry.go
type Registry struct {
	mu      sync.Mutex
	closing bool
	byUser  map[uuid.UUID]map[*Conn]struct{} // one user, several devices
	wg      sync.WaitGroup
}

func NewRegistry() *Registry {
	return &Registry{byUser: make(map[uuid.UUID]map[*Conn]struct{})}
}

// Add reports false once shutdown has begun, so the caller closes the socket instead of
// serving it. wg.Add(1) sits in the same critical section as the closing flag: that is what
// keeps Wait safe (a WaitGroup must never see an Add while Wait runs at zero).
func (r *Registry) Add(c *Conn) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closing {
		return false
	}
	if r.byUser[c.UserID] == nil {
		r.byUser[c.UserID] = make(map[*Conn]struct{})
	}
	r.byUser[c.UserID][c] = struct{}{}
	r.wg.Add(1)
	return true
}

func (r *Registry) Remove(c *Conn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.byUser[c.UserID], c)
	if len(r.byUser[c.UserID]) == 0 {
		delete(r.byUser, c.UserID)
	}
	r.wg.Done()
}

// Connected reports whether the user has at least one open socket on this replica: the
// publisher's cue to fall back to a push notification, and the test's cue that Add ran.
func (r *Registry) Connected(userID uuid.UUID) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.byUser[userID]) > 0
}

// Notify fans one signal out to every device of one user. Send never blocks, so holding
// the lock across the loop is fine.
func (r *Registry) Notify(userID uuid.UUID, s Signal) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for c := range r.byUser[userID] {
		c.Send(s)
	}
}

// CloseAll marks the registry closing and drops every connection with GoingAway. It never
// blocks (Drop only cancels), so it is safe as a srv.RegisterOnShutdown callback, and it is
// idempotent, so run() calls it again synchronously right before Wait.
func (r *Registry) CloseAll() {
	r.mu.Lock()
	r.closing = true
	conns := make([]*Conn, 0, 64)
	for _, set := range r.byUser {
		for c := range set {
			conns = append(conns, c)
		}
	}
	r.mu.Unlock()
	for _, c := range conns {
		c.Drop(websocket.StatusGoingAway, "server shutting down")
	}
}

// Wait blocks until every handler has returned, or until ctx expires.
func (r *Registry) Wait(ctx context.Context) error {
	done := make(chan struct{})
	go func() { r.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
```

The delta to `run()` in `golang-bootstrap`: the registry is built in the composition root like the pool and the river client, the server learns about it once, and the sequence gains a step between 1 and 2.

```go
	registry := ws.NewRegistry()
	srv := &http.Server{
		Addr:    net.JoinHostPort("", cfg.Port),
		Handler: api.NewRouter(users, auth, health, api.NewEventsResource(registry, cfg.WSOrigins)),
		// ReadHeaderTimeout, ReadTimeout, WriteTimeout, IdleTimeout exactly as in golang-bootstrap
	}
	srv.RegisterOnShutdown(registry.CloseAll) // early notifier: sockets start closing while REST drains

	// ... listener goroutine, signal wait, stop(), shutdownCtx exactly as in golang-bootstrap ...

	if err := srv.Shutdown(shutdownCtx); err != nil { // 1. HTTP: listeners, idle, in-flight REST
		return fmt.Errorf("http shutdown: %w", err)
	}
	registry.CloseAll()                                // 1b. Shutdown may return before the notifier even ran
	if err := registry.Wait(shutdownCtx); err != nil { //     every socket handler has returned
		slog.WarnContext(shutdownCtx, "websocket drain incomplete", "err", err) // a stuck socket must not skip river's Stop
	}
	if err := riverClient.Stop(shutdownCtx); err != nil { // 2. river, unchanged
		return fmt.Errorf("river stop: %w", err)
	}
	pool.Close() // 3. pool, unchanged
```

- **The drain sits between steps 1 and 2, never after.** Sockets are HTTP work that `Shutdown` refuses to finish, so they finish in step 1's slot, on the same `shutdownCtx` budget. Placed after `pool.Close`, anything a handler still does against the pool (a membership check when a signal arrives, an audit row) turns into a connection error; placed after river's `Stop`, it delays jobs for nothing.
- **Both `CloseAll` calls are load-bearing.** The `RegisterOnShutdown` one starts the close handshakes while REST requests drain, in parallel, so the total stays short. The synchronous one closes a race the notifier cannot: `Shutdown` returns as soon as the tracked connections are idle, possibly before its `go f()` has run, and a handler between `Accept` and `Add` at that instant would `wg.Add(1)` while `Wait` runs at zero, which `sync.WaitGroup` forbids. After the synchronous call `closing` is true before `Wait` starts, so a late `Add` returns false and that handler closes with `StatusGoingAway` on its own.
- **`Wait` logs and continues.** A timeout there means a few sockets are stuck in the library's bounded close waits (a dead peer costs 5s, then the socket is cut); river's `Stop` still has in-flight jobs to finish and must run. This is the one place the sequence in `golang-bootstrap` tolerates an error instead of returning it.
- **`CloseAll` never blocks**, which is the contract `RegisterOnShutdown` demands. `Drop` only records a reason and cancels a context; the handshake happens in each handler's own goroutine, all in parallel. `CloseAll` copies the set out under the lock and drops outside it, so a `Drop` that ever grows a side effect cannot deadlock against `Remove`.
- **The registry holds a `sync.WaitGroup` and a `sync.Mutex`: pass it by pointer, always.** A copy silently forks the counter, and `go vet`'s copylocks check will say so.
- `Registry` is where multi-device works: one user, several phones, one `Notify`. It is also the boundary of a single replica; with two, the signal has to reach the other process before it can reach that process's registry (out of scope here).

## Origin: `OriginPatterns`, never `InsecureSkipVerify`

The gorilla tutorial line `CheckOrigin: func(r *http.Request) bool { return true }` disables origin checking entirely and is the textbook Cross-Site WebSocket Hijacking hole: a page on any origin can open a socket to the API from a victim's browser, carrying whatever ambient credential the browser attaches. coder/websocket makes the safe thing the default and the unsafe thing loud:

- **With no `OriginPatterns`, `Accept` allows only the request's own host** for any request that carries an `Origin` header. Browsers always send one.
- **A request with no `Origin` header is allowed.** Native clients (OkHttp, `URLSession`, Dart's `WebSocket`) send none, so the mobile app connects with `OriginPatterns` empty. This also means the origin check is a browser-only defense (CSWSH), not authentication: any non-browser client can forge or omit the header, and the bearer token is what authenticates.
- **When a browser client appears, list its origins explicitly**: `OriginPatterns: []string{"app.example.com"}` (host patterns via `path.Match`, case-insensitive; `"https://app.example.com"` pins the scheme too). The field's own doc forbids `"*"`.
- **`InsecureSkipVerify: true` is `CheckOrigin: return true` under a name that admits it.** Never in production, and never in a snippet that could be copied into production; a local test that needs it says why in a comment.

## Testing a WebSocket handler

`httptest.NewRecorder` is not an `http.Hijacker`, so `Accept` never upgrades against it (501 for a well-formed handshake, 400 or 426 before that for a malformed one). A socket handler is tested through `httptest.NewServer`, which is the case `golang-testing` §4 reserves it for (the code needs a real URL), and `Dial` accepts the server's `http://` URL as is.

```go
func TestConnect_SignalReachesAuthenticatedClient(t *testing.T) {
	reg := ws.NewRegistry()
	srv := httptest.NewServer(api.NewRouter(users, auth, health, api.NewEventsResource(reg, nil)))
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, resp, err := websocket.Dial(ctx, srv.URL+"/ws", nil); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("dial without a token: err=%v resp=%v", err, resp) // RequireAuth answered, with the envelope
	}

	hdr := http.Header{"Authorization": {"Bearer " + signTestToken(t, userID)}}
	c, _, err := websocket.Dial(ctx, srv.URL+"/ws", &websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.CloseNow() }) // registered after srv.Close, so it runs first

	// Dial returns on the 101, which Accept writes before the handler reaches Add.
	waitConnected(t, reg, userID) // polls reg.Connected(userID) for up to a second
	reg.Notify(userID, ws.Signal{Kind: "account.updated", ResourceID: "acc_1", At: time.Now()})

	var got ws.Signal
	if err := wsjson.Read(ctx, c, &got); err != nil || got.Kind != "account.updated" {
		t.Fatalf("signal = %+v, err = %v", got, err)
	}
}
```

- **Close the client before the server.** `httptest.Server.Close` does not wait for hijacked connections either, so a client left open leaves a handler goroutine running past the test; `t.Cleanup` runs last-in first-out, so register the client's `CloseNow` after `srv.Close`. `goleak` (see `golang-patterns`) is what flags the leak if the order slips.
- **Every lifecycle claim gets a test**: a `Dial` without a bearer gets the 401 envelope; a signal reaches an authenticated client; `CloseAll` then `Wait` returns in about a millisecond and the client reads a 1001 frame; `CloseAll` is idempotent and a late `Add` is refused; 20,000 `Notify` calls against a client that never reads end in a 1008 frame and `Wait` still returns; a token with a 300ms `exp` ends in a 4001 frame; a client that keeps TCP open but never reads is dropped by the keepalive with a 1008 frame and `Wait` returns in `pongTimeout` plus 5s; a 100ms `middleware.Timeout` placed above the socket group leaves the connection alive and logs a 101. These eight ran under `-race` to write this skill, and `-race` is not optional here: the registry, `Drop`, and the loops are the concurrent surface of the service.
- **`-race` across the board**: `go test -race ./...`, per `golang-testing`.

## Anti-Patterns

- **`gorilla/websocket` for new code**, or `nhooyr.io/websocket` as an import path. Dormant, and deprecated, respectively.
- **`CheckOrigin: func(*http.Request) bool { return true }`** in any form, including `InsecureSkipVerify: true` and `OriginPatterns: []string{"*"}`. CSWSH.
- **The upgrade route under `middleware.Timeout`**, whether at the root or in its own group. A 60s deadline on the request context that a session must not inherit, plus a 504 written into a hijacked connection that chi's writer silently drops.
- **A keepalive that returns on a `Ping` error without closing.** The library leaves the socket open on a pong timeout; the handler, its goroutines, and the registry slot then live on until the token expires.
- **`r.Context()` as the parent of anything after `Accept`.** The library's doc warns against it; the request context is not cancelled when the peer leaves a hijacked connection, and it is cancelled by whatever middleware deadline the route inherited.
- **A session or deadline context handed to `Write` or `Ping`.** The library closes the socket the moment it ends, without a close frame. Per-operation contexts come from `Background` with a short timeout.
- **A second `Close` on the same connection** (`go c.Close(...)` from a publisher, a `Close` inside `CloseAll`). It parks for up to 15s and returns `net.ErrClosed`; all roads go through `Drop`, and the handler sends the frame.
- **A blocking send to the outbound channel**, an unbounded buffer, or a retry loop in `Send`. The publisher is a service method that can afford to block on nothing.
- **State in a frame.** A balance, a transaction row, a list. Signals carry a kind and an id; the client reads REST.
- **`Notify` inside the transaction that caused it.** The phone can read before the commit is visible.
- **`RegisterOnShutdown` as the whole shutdown story.** It is a notifier the server never waits for; without the registry's `Wait` in `run()`, `pool.Close` races the handlers.
- **`RegisterOnShutdown` as the only `CloseAll`.** `Shutdown` can return before the notifier runs; call `CloseAll` again synchronously before `Wait`.
- **`Registry` by value**, or a `sync.WaitGroup` inside any struct that gets copied. `go vet` copylocks.
- **A `ResponseWriter` wrapper that implements neither `Hijack` nor `Unwrap`** anywhere above the socket route. chi's own wrappers (`Logger`, `Compress`) forward `Hijack` and `Accept` follows `Unwrap`, so the chain in `chi-patterns` is fine; a hand-rolled wrapper that swallows both turns every upgrade into a 501.
- **`httptest.NewRecorder` against the socket handler.** Not a `Hijacker`; 501.
- **A ping interval longer than the proxy's read timeout.** nginx defaults `proxy_read_timeout` to 60s and closes the idle socket first, and the client sees a drop it cannot explain.
- **The JWT in `Sec-WebSocket-Protocol`** for a browser client. A one-time ticket from an authenticated REST call instead.

## Related Skills

- `chi-patterns`: `NewRouter`, `RequireAuth` and the authenticated group the socket route reuses, the envelope that `Accept`'s own errors fall outside of, and the per-group `middleware.Timeout` that the socket group leaves out.
- `golang-bootstrap`: `run()`, the shutdown sequence this skill inserts step 1b into, `RegisterOnShutdown`, and the rule that the registry is built in the composition root.
- `golang-patterns`: `select`, `sync.WaitGroup`, `sync.Once`, the context rules behind the three-context split, goroutine leaks (why `Wait`'s helper goroutine and every loop here has an exit path), `goleak`.
- `sqlc-patterns`: the pool that step 3 closes, and the transaction a publisher commits before it calls `Notify`.
- `golang-testing`: `httptest.NewServer` (§4), `-race`, and the fixtures behind `signTestToken`.
- `version-standards`: Go 1.27.0 and the exact-version rule that applies to `github.com/coder/websocket v1.8.15`.
- `redis-patterns`: pub/sub as one way to carry a signal across replicas, if that day comes.
