---
name: golang-patterns
description: Core Go idioms for this stack, written for a developer coming from Java/OOP. Covers the mental-model correction (no classes, no inheritance, structs are data plus receivers, interfaces satisfied implicitly), GoF patterns re-expressed as interfaces plus composition, struct embedding and method promotion, functional options, error handling (sentinels, %w, errors.Is/As, custom types, panic vs error), concurrency (goroutines, channels, select, sync, context, worker pools, fan-out/fan-in, errgroup), generics and when NOT to use them, plus the toolchain (go.mod/go.sum, module versioning, golangci-lint v2, gofmt/goimports) and package layout. Activate for any Go code, struct or interface design, error wrapping, goroutines or channels, context, generics, go.mod work, or golangci-lint configuration.
origin: ECC
---

# Go Patterns

The deepest Go reference on this stack: it plays the role `node-typescript-patterns` and `node-typescript-toolchain` play together for Node/TS, since Go's toolchain surface is small enough to fold into one skill.

```bash
go version         # the toolchain
head -3 go.mod     # the `go` directive: this is the LANGUAGE version, and it changes semantics
```

Read both before applying anything version-specific. Baseline is Go 1.27 (2026-08-19); the supported line is 1.27 and 1.26.

## Role and boundaries (which skill to use)

This skill = the language: types, interfaces, errors, concurrency, generics, modules, linting, layout. Route elsewhere for **tests** (table-driven, fakes, benchmarks, race detector) → `golang-testing`; **HTTP routing, middleware, handlers** → `chi-patterns`; **SQL and the data layer** → `sqlc-patterns` (Go does not use Prisma here, do not port `prisma-patterns`); **cross-cutting error taxonomy and response envelopes** → `error-handling`, `api-design`.

## When to Activate

- Writing or reviewing any `.go` file.
- Designing a struct, an interface, or a package boundary.
- Anything touching `go`, `chan`, `select`, or `context.Context`.
- Choosing between an interface, a type parameter, and reflection.
- Editing `go.mod`, `.golangci.yml`, or CI steps for a Go repo.

## Core Concepts

### 1. The OOP correction (read this first)

Go has **no classes, no inheritance, no `super`, no constructors, no overloading**. The Java instinct to model with a class hierarchy is the largest single source of bad Go.

| Concept | Java | Go |
|---|---|---|
| Data holder | `class` with fields | `struct`: plain data, like C |
| Behavior on data | methods inside the class body | methods declared **outside** the type with a receiver, like Rust `impl` blocks |
| Contract | `interface` + `implements` | `interface` satisfied **implicitly / structurally**, no keyword, unlike Rust's explicit `impl Trait for Type` |
| Reuse | `extends` a base class | **composition**: embed a struct, or hold a dependency in a field |
| Polymorphism | override a virtual method | pass a different implementation of an interface |

Implicit satisfaction has no analogue in Java or Rust. A type satisfies an interface the moment its **method set** matches: it never names the interface, never imports the declaring package, and can satisfy an interface written years later by someone else. Hence **"define interfaces where they are consumed, not where they are implemented"**: the consumer declares the 1 to 3 methods it needs, and every type that already has them fits.

```go
// BEFORE: Java-style inheritance transliterated into Go, and it does not work
type BaseNotifier struct{ from string }
func (b *BaseNotifier) Send(to, body string) error {
    return b.deliver(to, body) // impossible: deliver() cannot be abstract, and an
}                              // embedder's deliver() would NEVER be called from here

// AFTER: a consumer-side interface plus composition
type Deliverer interface { // the CONSUMER declares what it needs; nothing "implements" this
    Deliver(ctx context.Context, to, body string) error
}
type Notifier struct { // shared behavior HOLDS a dependency instead of extending a superclass
    from string
    out  Deliverer
}
func New(from string, out Deliverer) *Notifier { return &Notifier{from: from, out: out} } // ctor = func
func (n *Notifier) Send(ctx context.Context, to, body string) error {
    if err := n.out.Deliver(ctx, to, body); err != nil { return fmt.Errorf("notify %s: %w", to, err) }
    return nil
}
type Email struct{ host string }
func (e Email) Deliver(ctx context.Context, to, body string) error { /* ... */ return nil }
var _ Deliverer = Email{} // optional assertion: the only place the link is ever written
```

**Receivers.** Use a pointer receiver when the method mutates, when the struct is large, or when any other method on the type already uses one (never mix receiver kinds on one type). Pointer-receiver methods belong to the method set of `*T` **only**: had `Deliver` been declared on `*Email`, `var _ Deliverer = Email{}` would fail to compile and only `&Email{}` would satisfy it. A nil pointer receiver is legal and callable, which feeds the typed-nil trap below.

### 2. Composition via struct embedding

Embedding promotes the inner type's fields and methods. That is **method promotion, not inheritance**:

```go
type Base struct{}
func (Base) Name() string       { return "base" }
func (b Base) Describe() string { return "I am " + b.Name() } // b is a Base, statically
type Child struct{ Base }                     // embedded, no field name
func (Child) Name() string { return "child" } // "overrides" Name for Child callers
Child{}.Name()     // "child"
Child{}.Describe() // "I am base"   <-- Java would print "I am child"
```

There is **no virtual dispatch back into the outer type**: `Describe` was compiled against `Base` and calls `Base.Name` forever, so any "template method calls an overridden hook" design is broken in Go. Express it with an interface field (`Notifier.out` above), which *is* dynamically dispatched. Also: a name collision at equal depth errors only when the ambiguous selector is used, the shallowest name wins, and embedding `sync.Mutex` exports `Lock`/`Unlock` on your type (prefer a named `mu sync.Mutex` field).

### 3. GoF patterns, re-expressed

**Factory**: a function returning an interface. The sanctioned exception to "accept interfaces, return structs", since a constructor for one concrete type returns that struct while a factory that *chooses* an implementation returns the interface. **Strategy**: a function type when stateless, an interface when it needs state or several methods. No `AbstractStrategy`.

```go
func NewStore(kind, dsn string) (Store, error) { // Factory
    switch kind {
    case "postgres": return newPostgres(dsn) // (*postgresStore, error), unexported concrete type
    case "memory":   return newMemory(), nil
    default:         return nil, fmt.Errorf("unknown store kind %q", kind)
    }
}

type PriceRule func(o Order) int64 // Strategy: no interface needed when it is stateless
func Total(o Order, rules ...PriceRule) int64 {
    var cents int64
    for _, rule := range rules { cents += rule(o) }
    return cents
}
Total(o, BasePrice, VolumeDiscount, TaxFor("CO")) // TaxFor returns a closure: parameterized strategy
```

**Decorator**: wrap an interface value in a type embedding that same interface, so unrelated methods stay promoted and adding a method to `Store` later does not break every wrapper.

```go
type cachedStore struct {
    Store // embedded INTERFACE: every other method is promoted to the wrapper
    mu    sync.RWMutex
    cache map[string]User
}
func (s *cachedStore) Get(ctx context.Context, id string) (User, error) {
    s.mu.RLock(); u, ok := s.cache[id]; s.mu.RUnlock()
    if ok { return u, nil }
    u, err := s.Store.Get(ctx, id) // delegate to the wrapped value
    if err != nil { return User{}, err }
    s.mu.Lock(); s.cache[id] = u; s.mu.Unlock()
    return u, nil
}
```

Cost of that convenience: a nil embedded interface panics at call time instead of failing at construction, so validate in the constructor. Over HTTP this degenerates into the middleware idiom (`func(http.Handler) http.Handler`), covered in `chi-patterns`.

### 4. Functional options, and when not to

**Default to a plain config struct**: fewer lines, greppable, decodable from JSON/env, visible as data in godoc. Options earn their complexity only when the constructor is **public library API** where new knobs must not break call sites, an option can **fail validation** and must return an error, options must set **unexported fields** from another package, or "unset" must be distinguishable from "set to the zero value". For an internal constructor called from three places, a `Config` struct wins. Never ship both: two ways to configure one type is two sources of truth.

```go
type Option func(*Server) error // returns error so an option can reject bad input
func WithTimeout(d time.Duration) Option {
    return func(s *Server) error {
        if d <= 0 { return fmt.Errorf("timeout must be positive, got %s", d) }
        s.timeout = d
        return nil
    }
}
func NewServer(addr string, opts ...Option) (*Server, error) { // addr is positional: required
    s := &Server{addr: addr, timeout: 5 * time.Second, maxConns: 100} // defaults FIRST
    for _, opt := range opts {
        if err := opt(s); err != nil { return nil, fmt.Errorf("server option: %w", err) }
    }
    if err := s.validate(); err != nil { return nil, err } // validate AFTER every option applied
    return s, nil
}
```

Common mistakes: applying defaults *after* the options (silently discarding the caller's input), `Option func(*Server)` with no error *on an option that can reject its input*, so bad values are swallowed, exporting the struct fields as well so options can be bypassed, and smuggling required parameters in as options (`addr` is positional above for exactly that reason).

### 5. Error handling

Errors are values. No `throw`, no checked exceptions, no unwinding in normal control flow.

```go
var ErrNotFound = errors.New("user: not found")     // sentinel: package-level, public API
type ValidationError struct{ Field, Reason string } // typed error: when callers need detail
func (e *ValidationError) Error() string { return e.Field + ": " + e.Reason }

func (r *Repo) Get(ctx context.Context, id string) (User, error) {
    u, err := r.db.Load(ctx, id)
    if errors.Is(err, sql.ErrNoRows) { return User{}, fmt.Errorf("get user %s: %w", id, ErrNotFound) }
    if err != nil { return User{}, fmt.Errorf("get user %s: %w", id, err) } // %w keeps the chain
    return u, nil
}
// Caller side
if errors.Is(err, ErrNotFound) { return c.NotFound() } // identity: walks the whole %w chain
var ve *ValidationError
if errors.As(err, &ve) { return c.BadRequest(ve.Field, ve.Reason) } // sets ve to the first match
```

- **`%w` wraps, `%v` flattens.** `fmt.Errorf("...: %v", err)` compiles fine and silently destroys `errors.Is`/`As` for every caller above. Use `%v` only to deliberately hide a cause (not leaking a driver error across a public boundary).
- **Add context, do not narrate.** Wrap where you know something the callee did not (the id, the operation); wrapping at every frame produces `handler: service: repo: query: scan: EOF`. Multiple `%w` verbs in one `Errorf` are legal (1.20+) and yield a multi-error, and `errors.Join(errs...)` aggregates independent failures.
- **Never `==` against a possibly-wrapped sentinel and never compare error strings.** `errors.Is` is the only correct test. Sentinel when callers only branch, typed error when they need fields.
- **`panic` is for programmer bugs, not for failure.** Legitimate: impossible states, invalid package init, `MustCompile`-style helpers over constants. Illegitimate: bad input, network failure, missing row. Never let a panic cross a package boundary; a server recovers once in the outermost middleware and returns a 500 with a logged stack. `recover()` works only in a deferred function **on the same goroutine**, so a panic inside a bare `go func()` kills the process regardless of what the caller wrapped.

### 6. Concurrency

The feature with no Node equivalent: Node is single-threaded and cooperatively async, Go runs real parallel goroutines across OS threads, so shared memory is genuinely shared.

**Goroutines.** Since Go 1.25 use `wg.Go`, which replaces the `Add(1)` / `go func()` / `defer Done()` triple (`go tool fix` ships a `waitgroupgo` rewriter). **Channels** are typed and unbuffered by default; the **sender closes**, never the receiver, and never twice (both panic). Use directional types in signatures (`<-chan T`, `chan<- T`) so the compiler enforces the role; a nil channel blocks forever, which usefully disables one arm of a `select`. Every blocking operation in production selects on cancellation.

```go
var wg sync.WaitGroup
for _, u := range urls { wg.Go(func() { warm(ctx, u) }) } // Go 1.25+; no `u := u` on a go1.22+ module
wg.Wait()

select {
case res := <-work:  return res, nil
case <-ctx.Done():   return nil, ctx.Err() // or context.Cause(ctx) for the richer reason
}
```

**sync.** "Share memory by communicating" is the slogan, but a plain `sync.Mutex` around a map or counter is idiomatic and faster than a channel. `RWMutex` only when reads truly dominate; `sync.Once` for lazy init, or `sync.OnceValue`/`OnceValues` (1.21+) which return the computed value; `atomic.Int64` for counters. Never copy a struct holding a mutex (`go vet` catches it).

**context.Context.** First parameter, always named `ctx`, never stored in a struct. Derive with `ctx, cancel := context.WithTimeout(ctx, 5*time.Second)` and `defer cancel()` **always**, including the success path, since skipping it leaks the timer and the child context. `ctx.Err()` reports `context.Canceled` or `context.DeadlineExceeded`, while `context.Cause(ctx)` gives the richer reason when the tree was built with `context.WithCancelCause`. Values carry request-scoped metadata only (trace id, auth subject), never optional parameters or dependencies, and the key must be an unexported struct type, never a bare string.

**Worker pool (bounded, cancelable, fan-out then fan-in):**

```go
in := make(chan Job, len(jobs))
for _, j := range jobs { in <- j }
close(in) // the producer owns `in` and closes it; with an unbounded/streaming source, feed it
          // from a separate goroutine that also selects on ctx.Done() instead of pre-filling
out := make(chan Result, len(jobs))
var wg sync.WaitGroup
for range runtime.NumCPU() { // range-over-int, Go 1.22+
    wg.Go(func() {
        for j := range in { // each worker drains until `in` is closed
            select {
            case out <- do(ctx, j):
            case <-ctx.Done(): return
            }
        }
    })
}
go func() { wg.Wait(); close(out) }() // fan-in: exactly one goroutine may close `out`
for r := range out { results = append(results, r) }
```

**errgroup** (`golang.org/x/sync/errgroup` v0.22.0) when the fan-out needs first-error propagation, cancellation, or a concurrency cap. Plain `sync.WaitGroup` covers everything else, so do not add the dependency reflexively.

```go
g, gctx := errgroup.WithContext(ctx)
g.SetLimit(8)                 // ONCE, before the first Go; changing it with work in flight panics
docs := make([]Doc, len(ids))
for i, id := range ids {      // no `id := id` shadow copy on a go1.22+ module
    g.Go(func() error {
        // REQUIRED: Go() never selects on Done, so tasks keep starting after a failure
        if err := gctx.Err(); err != nil { return err }
        d, err := fetch(gctx, id)
        if err != nil { return fmt.Errorf("doc %s: %w", id, err) }
        docs[i] = d           // one distinct slot per goroutine, so no mutex is needed
        return nil
    })
}
if err := g.Wait(); err != nil { return nil, err }
```

Contract details visible only in the source, each one a real bug in the wild:

- `Wait` returns the **first** non-nil error (stored under a `sync.Once`), never a join. Collect per-index errors yourself and `errors.Join` them when the caller needs all failures.
- `Wait` cancels `gctx` **before** returning, so after `Wait` that context is dead: use the parent `ctx` for cleanup, follow-ups, or a second group.
- Inside a worker, `context.Cause(gctx)` returns the group's first error while `gctx.Err()` only says `context.Canceled`, which distinguishes "a sibling failed with X" from "the parent deadline expired".
- Panics are deliberately **not** recovered or propagated, so a panicking task crashes the process. Add `defer recover()` inside the func and convert to an error when tasks run risky work.
- A zero `Group` is valid but has no cancellation (only `WithContext` provides it), a group must not be reused, and `SetLimit(0)` blocks every `Go` forever.

### 7. Generics (1.18+), and when not to

The governing rule is still Ian Lance Taylor's: *write Go programs by writing code, not by defining types*. A design that starts with constraints started wrong. Constrain on **`cmp.Ordered` from the standard library**, never `golang.org/x/exp/constraints` (its own docs call it redundant since Go 1.21, so seeing it dates the code).

```go
type Number interface{ ~int | ~int64 | ~float64 } // `~` admits named types like `type Celsius float64`
func Sum[T Number](xs []T) T {
    var total T
    for _, x := range xs { total += x }
    return total
}
```

Do **not** reach for a type parameter when all the code does is call a method on the value (`func Log(v fmt.Stringer)` beats `func Log[T fmt.Stringer](v T)`), when the implementation differs per type (that is interface dispatch), when the types share no methods and no shape (that is reflection), or when **the standard library already did it**: `slices` ships roughly 45 generic functions (`Sort`, `SortFunc`, `Contains`, `Index`, `Equal`, `BinarySearch`, `Clone`, `Compact`, `Chunk`, `Reverse`, `Collect`, `Sorted`), `maps` has `Keys`, `Values`, `Clone`, `Equal`, `DeleteFunc`, and `min`, `max`, `clear` are generic builtins, so nearly every hand-rolled `Map`/`Filter`/`Keys`/`Contains` helper from 2022 is dead code (`go tool fix` ships the rewriters `slicessort`, `slicescontains`, `mapsloop`, `minmax`, `rangeint`). Do reach for one when the code is byte-identical across types: generic containers (trees, LRUs, queues) and helpers over slices/maps/channels that ignore the element type.

**New in Go 1.27, and a fresh footgun.** Methods may now declare their own type parameters, but interface methods may not, and *a generic method cannot satisfy an interface method*. Adding type parameters to an existing method silently drops it from every interface that type used to satisfy:

```go
type Scaler interface{ Scale(int) int }
func (b *Box) Scale[T ~int | ~float64](f T) T { /* ... */ } // now generic
var _ Scaler = &Box{}
// compile error: *Box does not implement Scaler (wrong type for method Scale)
//   have Scale[T ~int | ~float64](T) T / want Scale(int) int
```

## Toolchain

**gofmt and goimports are non-negotiable**: no style debate, no project-specific formatting. What the rules layer does not say is that they are not interchangeable. `gofmt` only rewrites the code it is given; `goimports` is a superset that additionally **adds the imports the file needs and deletes the ones it does not**, and regroups them into stdlib and non-stdlib blocks, with `-local github.com/tunodo-organization` pushing your own modules into a third block. Configure `goimports` under `formatters` (never `linters`, see the v2 block below), format on save, and enforce it in CI as a diff check (`test -z "$(gofmt -l .)"`) so a misformatted file fails the build instead of being silently rewritten by the next person's editor. `golangci-lint fmt` (v2) applies every configured formatter in one shot, and `golangci-lint fmt --diff` is the CI-side check that covers all of them at once.

```
module github.com/tunodo-organization/svc
go 1.27              // LANGUAGE version: gates semantics, see the loop-variable anti-pattern
toolchain go1.27.0   // optional pin of the toolchain that builds it
require golang.org/x/sync v0.22.0
```

- `require` versions are exact by construction (Go has no `^`/`~` ranges), so the global exact-version rule holds automatically. Bump with `go get module@version`.
- `go.sum` is a checksum lock (hashes, not a version list), verified against the checksum database. **Commit both files.**
- `go mod tidy` after any import change, `go mod verify` to check the cache, `go mod why -m <mod>` to find who pulled a dependency in. CI builds with `-mod=readonly` (default since 1.16), so a stale `go.mod` fails instead of being rewritten.
- **Semantic import versioning**: v2 and above require the major in the module path (`example.com/lib/v2`). A library that bumps to v2 without changing the path is broken for consumers.

**golangci-lint** v2.13.1. The v2 file has exactly seven top-level keys: `version`, `run`, `linters`, `formatters`, `issues`, `output`, `severity`.

```yaml
version: "2"          # required, string
run: { timeout: 5m }
linters:
  default: standard   # tool default = errcheck, govet, ineffassign, staticcheck, unused
  enable:             # deliberate production layer on top; gosec is OPT-IN, never a default
    - errorlint       # %v where %w belongs, wrong errors.Is/As usage
    - bodyclose       # unclosed http.Response.Body
    - noctx           # HTTP requests issued without a context
    - copyloopvar     # flags the now-redundant `v := v`
    - intrange        # 3-clause loops that should be range-over-int
    - nilerr          # `return nil` after checking err != nil
    - gosec           # wherever code touches crypto, subprocess, file paths, or network input
    - revive
    - gocritic
    - unconvert
    - usetesting
  settings:
    staticcheck: { checks: ["all"] } # SA/S/ST selection lives HERE now
  exclusions:
    generated: lax                   # strict | lax | disable
    rules:
      - path: _test\.go
        linters: [gosec, errcheck]
formatters:           # v2: formatters are their OWN top-level block, not linters.enable
  enable: [goimports]
```

v1 habits that now hard-fail: `enable-all`/`disable-all` (now `linters.default: all|none`), `gofmt`/`goimports` under `linters.enable` (now `formatters`), `linters-settings` (now `linters.settings` plus `formatters.settings`), `run.skip-dirs`/`skip-files` (now `linters.exclusions.paths`), `issues.exclude-rules` (now `linters.exclusions.rules`). **`gosimple` and `stylecheck` no longer exist**, both merged into `staticcheck`, and naming them errors out. Also removed: `deadcode`, `structcheck`, `varcheck`, `golint`, `interfacer`, `maligned`, `scopelint`, `exportloopref`, `tenv`, plus the `gas`/`gomnd` aliases. Breaking linter upgrades now ship as suffixed twins with the old name deprecated (`exhaustruct_v5`, `wsl_v5`, `gomodguard_v2`). `golangci-lint migrate` converts a v1 file in place but drops every comment. **Security scanning** is `gosec` (above, or standalone `gosec ./...`) plus `govulncheck ./...` for known CVEs; secrets come from the environment and are checked at startup, never from a literal.

## Package and project layout

Start flat. The official guidance (go.dev/doc/modules/layout) opens with all code in the module root: `go.mod`, `svc.go`, `svc_test.go`. Grow directories only when a need actually appears.

```
svc/
  go.mod  go.sum
  main.go                              # single binary: main at the root is fine
  internal/auth/  internal/store/      # compiler-enforced: no other module can import these
  cmd/api/main.go  cmd/worker/main.go  # ONLY with 2+ binaries, or binaries mixed with libraries
```

- **`internal/` is enforced by the compiler**: an import from outside the module fails with `use of internal package ... not allowed`. That freedom to refactor is why server projects keep nearly everything there. **`cmd/` is conditional**, not mandatory: one binary means `main.go` at the root.
- **Do not create `pkg/`.** The toolchain gives it no meaning, the official layout page never mentions it, and Russ Cox rebutted `golang-standards/project-layout` directly (issue #117, "this is not a standard Go project layout") while that repo's own README disclaims official status. If a package is genuinely reusable, split it into its own module instead of advertising it under `pkg/`.
- Package names are short, lowercase, singular, no underscores, never `util`/`common`/`base`. The name is part of every call site: `store.New()` reads well, `stores.NewStore()` stutters.

## Anti-Patterns

### Typed nil in an interface is not nil

```go
type MyErr struct{}
func (*MyErr) Error() string { return "boom" }
func do() error {
    var e *MyErr // nil pointer
    return e     // interface holds (type=*MyErr, value=nil): NOT equal to nil
}
if err := do(); err != nil { /* THIS FIRES, always */ }
```

An interface value is a (type, value) pair and is nil only when **both** halves are. Fix: declare the variable as `error`, or `return nil` literally on the success path. Never return a concrete error pointer type from a function whose signature says `error`.

### Goroutine leaks

```go
ch := make(chan Result)      // FAIL: unbuffered
go func() { ch <- slow() }() // after the timeout nobody receives, so this blocks forever
select {
case r := <-ch:    return r, nil
case <-ctx.Done(): return Result{}, ctx.Err() // goroutine leaked, with everything it captured
}
```

Fix with `make(chan Result, 1)` so the send always completes, or make the producer select on `ctx.Done()`. Every goroutine needs a provable exit path: verify with `goleak` in tests (see `golang-testing`) and `runtime.NumGoroutine()` on a debug endpoint.

### Loop-variable capture: get the Go 1.22 rules right

In Go 1.22+ a loop variable declared with `:=` is a **new instance per iteration**, which killed the classic capture bug. The parts most write-ups still get wrong:

- **The gate is the effective per-file language version, not the toolchain.** The same source under the same Go 1.27 prints `[3 3 3]` when `go.mod` says `go 1.21` and per-iteration values when it says `go 1.22`. Installing a new toolchain changes nothing; **bumping the `go` directive is the risk event**. A `//go:build go1.21` constraint lowers the language version **for that one file**, silently restoring the old semantics inside an otherwise modern module.
- 3-clause loops still behave as before when the body mutates the variable, because the per-iteration copy is written back at the end of the body: `for i := 0; i < 10; i++ { if i == 2 { i = 7 } }` still runs 5 iterations.
- **The residual footgun**: the change covers only variables the loop *declares*. One declared outside and merely assigned by the loop is still shared, still `[3 3 3]`: `var j int; for j = 0; j < 3; j++ { ptrs = append(ptrs, &j) }`.
- `v := v` is dead weight on a go1.22+ module (`go tool fix` analyzer `forvar` and the `copyloopvar` linter both remove it), so sample code still writing `item := item` before `g.Go` is a pre-1.22 template. And `go vet`'s `loopclosure` check going quiet means the module opted in, not that the code was fixed.

### Ignoring errors

`_ = doThing()` and a bare `defer f.Close()` on a **writable** file both discard real failures (a failed `Close` on a buffered writer means the data never landed). Handle it, or capture it in a named return:

```go
func write(path string, b []byte) (err error) {
    f, err := os.Create(path)
    if err != nil { return fmt.Errorf("create %s: %w", path, err) }
    defer func() { err = errors.Join(err, f.Close()) }() // the Close error survives
    _, err = f.Write(b)
    return err
}
```

`errcheck` is in the default linter set precisely to catch the first form. `_ =` is acceptable only with a comment explaining why the error cannot matter.

## Best Practices

| Rule | Reason |
|---|---|
| Define interfaces at the consumer, 1 to 3 methods | Implicit satisfaction makes the consumer the only place that knows the contract |
| Accept interfaces, return concrete structs | Callers stay testable; a factory choosing an implementation is the only exception |
| Composition and interface fields, never a "base struct" | Embedding promotes methods but never dispatches back to the outer type |
| `ctx context.Context` first parameter, never a struct field; `defer cancel()` on every derived context | Cancellation follows the call, not the object; a skipped cancel leaks the timer |
| Wrap with `%w`, branch with `errors.Is`/`As` | `%v` silently breaks every caller's error inspection |
| The sender closes a channel, and exactly one goroutine closes it | Closing twice, or closing from the receiver, panics |
| Check `ctx.Err()` at the top of every errgroup task | `Go` never selects on `Done`, so work keeps starting after the first failure |
| Reach for `slices`/`maps`/`cmp` before writing a type parameter | The standard library already absorbed the common generic cases |
| `internal/` yes, `pkg/` no | One is compiler-enforced, the other is meaningless to the toolchain |
| gofmt/goimports in CI, golangci-lint v2 with `version: "2"` | Formatting is settled, and a v1 config now hard-fails |

## Related Skills

- `golang-testing` for table-driven tests, subtests, fakes, benchmarks, the race detector, and `goleak`
- `chi-patterns` for HTTP routing, middleware, and handler structure
- `sqlc-patterns` for the database layer (Go does not use Prisma on this stack)
- `error-handling` and `api-design` for the cross-cutting error taxonomy and response envelope
- `docker-patterns` for multi-stage builds of a static Go binary
- `version-standards` for the CI runner and pinned-version policy
