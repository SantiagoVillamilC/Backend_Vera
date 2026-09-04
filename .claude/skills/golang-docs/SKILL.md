---
name: golang-docs
description: Documentation for a Go service on this stack (chi v5 + sqlc/pgx against self-hosted PostgreSQL), written for a team arriving from Javadoc and Swagger annotations. Covers the godoc convention exactly as the official docs state it (a comment glued to a top-level declaration IS its documentation, it opens with the identifier's name as a sentence, no `@param`/`@return`/`@throws` tags, required on exported names and on non-trivial unexported ones, one package comment per package), the Javadoc-to-Go mapping, enforcing the convention with staticcheck's ST1000/ST1020/ST1021/ST1022 inside golangci-lint (off by the tool's default, already on through the `checks` list set to `all` in the `golang-patterns` config, silently switched off again by the `comments` exclusion preset), and API documentation spec-first with oapi-codegen v2, with `api/openapi.yaml` as the hand-written contract modeled on the `{success, data, error, meta}` envelope, `go tool oapi-codegen` pinned at v2.8.0 generating the types and the chi `ServerInterface`, handlers implementing that interface with no per-endpoint annotation block, validator tags injected through `x-oapi-codegen-extra-tags`, the `ErrorHandlerFunc` that keeps parameter errors inside the envelope, and the decision not to run the kin-openapi request validator because go-playground/validator already owns request validation. Activate when writing or reviewing a doc comment, a package comment or `doc.go`, enabling or tuning the comment linters, adding or changing an endpoint that must appear in the OpenAPI contract, running `go generate` for the API, or when someone proposes swag annotations, Javadoc-style tags, `huma`, or a second validation layer. Not for routing, the middleware chain, the envelope helpers, or `decodeAndValidate` (see `chi-patterns`), not for general Go idioms or the rest of `.golangci.yml` (see `golang-patterns`), not for sqlc's own generated code (see `sqlc-patterns`), not for boot, config, or health probes (see `golang-bootstrap`), and not for tests (see `golang-testing`).
---

# Go Docs

Two documents describe a Go service on this stack and both are code-adjacent: the doc comments that `go doc` extracts from the source, and the OpenAPI contract that the handlers are generated against. Neither is written the way Java or TypeScript taught the team. A Go doc comment is one or two sentences of prose, not a tag block, and it is only required where a reader can arrive from outside the package. An OpenAPI contract is a YAML file written by hand and reviewed in the PR, not annotations scraped from above each handler.

Versions are pinned in `version-standards`: Go **1.27.0**, chi **v5.3.2**, golangci-lint **v2.13.1**, go-playground/validator **v10.30.3**. This skill adds two pins that table does not carry yet: oapi-codegen **v2.8.0** (released 2026-07-17, generated code needs Go 1.24+) and its runtime **`github.com/oapi-codegen/runtime` v1.7.0**. Verified 2026-09-04 against go.dev/doc/comment, go.dev/wiki/CodeReviewComments, the staticcheck checks list, golangci-lint's `.golangci.reference.yml` plus an empirical run of v2.13.1, and the oapi-codegen v2.8.0 source and templates. The generator config, the contract, the envelope adaptation, `ParamError`, and the router delta below were generated, compiled, vetted, linted with the `golang-patterns` config, and exercised by four `httptest` cases against those versions; the resource and router blocks are abridged to what they demonstrate.

## Role and boundaries (which skill to use)

This skill = the doc-comment convention and its linters, plus the contract-to-handler pipeline. Everything the pipeline plugs into already has an owner:

- **The `{success, data, error, meta}` envelope, `WriteData` / `WriteError` / `WriteAPIError`, `decodeAndValidate`, go-playground/validator, the two router groups and the middleware order** → `chi-patterns`. This skill only says which generated call lands in which group and what the generated code hands to `WriteError`.
- **gofmt/goimports, the `.golangci.yml` file as a whole, package layout, error idioms** → `golang-patterns`. This skill names the one setting in that file the doc checks depend on; it does not restate the file.
- **sqlc's generated code** → `sqlc-patterns`. It carries the same `Code generated ... DO NOT EDIT.` header as the oapi-codegen output, and the same lint exclusion covers both.
- **`/healthz`, `/readyz`, `LoadConfig`, the composition root** → `golang-bootstrap`. Probes are orchestrator-facing and stay out of the OpenAPI contract.
- **A WebSocket upgrade route** → `golang-websocket`. OpenAPI 3.0 cannot describe an upgrade; the socket route stays hand-registered and documented in the package comment of the package that owns it.
- **Tests** for a handler that implements the generated interface → `golang-testing`.
- **Out of scope, on purpose**: generating a TypeScript client (the frontend generates it from the same `api/openapi.yaml` with its own tooling), oapi-codegen's `strict-server` mode, and hosting a Swagger UI.

## When to Activate

- Writing or reviewing any comment that sits directly above a `package`, `type`, `func`, `const`, or `var` declaration.
- A review comment asking for `@param`, `@return`, `@throws`, or "more documentation" on a private helper.
- Touching `staticcheck.checks`, `exclusions.presets`, or `revive` in `.golangci.yml`, or seeing `ST1000`/`ST1020`/`ST1021`/`ST1022`/`package-comments`/`exported` in lint output.
- Adding, renaming, or reshaping an endpoint, a request body, a query parameter, or a response, or editing `api/openapi.yaml` for any reason.
- Running `go generate ./...` for the API, bumping oapi-codegen, or a CI failure on a stale `openapi.gen.go`.
- Someone proposing `swaggo/swag`, `huma`, `OapiRequestValidator`, or `enable-auth-scopes-on-context`.

## Doc comments: what the rule actually says

The whole convention is three sentences from the official docs. go.dev/doc/comment: *"Doc comments are comments that appear immediately before top-level package, const, func, type, and var declarations with no intervening newlines. Every exported (capitalized) name should have a doc comment."* And on parameters: *"Named parameters and results can be referred to directly in the comment, without any special syntax like backquotes."* go.dev/wiki/CodeReviewComments adds the scope and the shape: *"All top-level, exported names should have doc comments, as should non-trivial unexported type or function declarations."* and *"Comments should begin with the name of the thing being described and end in a period."*

```go
// BEFORE: Javadoc transliterated. Every line here is either noise or wrong for Go.

/**
 * Transfers money between two wallets.
 *
 * @param ctx     the context
 * @param from    the source wallet id
 * @param to      the destination wallet id
 * @param cents   the amount in cents
 * @return the created transfer
 * @throws ErrInsufficientFunds if the source wallet cannot cover the amount
 */
func (s *Service) Transfer(ctx context.Context, from, to uuid.UUID, cents int64) (Transfer, error)

// AFTER: the name first, complete sentences, parameters and errors named in prose.
// (Blank line next: an annotation glued to the doc comment would become its first line.)

// Transfer moves cents from the wallet from to the wallet to inside one
// transaction and returns the resulting Transfer. It returns
// ErrInsufficientFunds when from cannot cover cents, and ErrSameWallet
// when from and to are equal; both are safe to match with errors.Is.
func (s *Service) Transfer(ctx context.Context, from, to uuid.UUID, cents int64) (Transfer, error)
```

The `/** ... */` block, the tags, the alignment, and the restated parameter list are all gone. What survives is what a reader cannot get from the signature: the transactional guarantee and the two errors worth branching on. `go doc ./internal/transactions` prints exactly this paragraph under the signature, and pkg.go.dev renders it the same way.

| Javadoc habit | Go |
|---|---|
| `@param x the x` on every parameter | Nothing. Name the parameter in prose only when its meaning is not obvious from the signature (`cents`, not `amount in cents`). |
| `@return` | "It returns ..." as part of the sentence, or the first sentence itself for a getter. |
| `@throws` | "It returns ErrX when ..." Sentinels and typed errors are values; name them so callers know what to `errors.Is`. |
| `@see Other` | A doc link: `[Service.Refund]` or `[dbstore.Querier]`. pkg.go.dev renders it as a link; `go doc` prints the brackets, which still read fine. |
| `@deprecated` | A paragraph starting with `Deprecated:`. It is the one marker go/doc and staticcheck (SA1019) both read. |
| `@author`, `@since`, `@version` | Nothing. `git blame` and tags already hold it, and it rots inside a comment. |
| A comment on every private method | Nothing on trivial ones. `envOr` needs no sentence; a 40-line unexported state machine does. |

- **Exported means the comment is mandatory; unexported means it is owed only when the name does not already say it.** The wiki's "non-trivial" is the whole test. A private helper whose comment would restate its name is better with no comment, since the linter will then have nothing to complain about and the reader nothing to skip.
- **The first sentence is the summary.** `go doc` and pkg.go.dev show it alone in listings, so it must stand on its own: `Transfer moves cents between two wallets.` reads in a list; `This function is used to ...` does not.
- **Comments document behavior, not implementation.** "Loops over the rows and appends" is what the body already says; "returns the rows ordered by created_at descending" is what the caller needs.
- **A blank line between the comment and the declaration detaches it.** It becomes a stray comment, `go doc` shows nothing, and ST1020 reports the function as undocumented. Same for `package`.

### Package comments

One per package, glued to the `package` clause in exactly one file. CodeReviewComments: *"Package comments, like all comments to be presented by godoc, must appear adjacent to the package clause, with no blank line."* When several files carry one, go/doc concatenates them, which is never what anyone wanted.

```go
// Package transactions moves money between wallets. Every write goes through
// Service.Transfer, which is the only place that opens a transaction, and the
// package exposes the sentinel errors a handler maps onto HTTP statuses.
package transactions
```

For a binary the wiki's forms are `// Command api ...`, `// Binary api ...`, or `// The api command ...` directly above `package main`. `cmd/api/main.go` is where a team meets ST1000 first, because nobody thinks of `main` as an API.

Isolating the package comment in a `doc.go` is a community habit, not a rule; the standard library does it for large packages (`net/http/doc.go`) and skips it for small ones. Use it when the comment runs longer than a paragraph, or when the package also carries a `//go:generate` directive, which is the case for `internal/api` below. gofmt then enforces the layout for you: go.dev/doc/comment states that *"Gofmt moves directive comments to the end of the doc comment, preceded by a blank line"*, and the post-edit hook in this workspace (`golangci-lint fmt`) does exactly that, turning the two lines into a paragraph, a bare `//`, then the directive.

### What gofmt does and does not check

gofmt rewrites the presentation of a doc comment (list markers, code-block indentation, directive placement, leading and trailing blank lines) and nothing else. It does not know that `// This helper adds cents.` above `func Add` is wrong, and neither does `go vet`. With golangci-lint on its tool defaults the file above lints clean: verified, `0 issues.` That silence is why the next section exists.

## Enforcing it: ST1000, ST1020, ST1021, ST1022 in golangci-lint

`golint`, the linter that used to enforce "comment must start with the name", is gone. Its README: *"Golint is deprecated and frozen. There's no drop-in replacement for it, but tools such as Staticcheck and go vet should be used instead."* The repository was archived on 2021-05-09 (golang/go#38968), and golangci-lint v2 no longer accepts `golint` as a linter name (`golang-patterns` lists it among the removed ones).

The replacement is four staticcheck checks, every one of them marked **non-default** on staticcheck.dev:

| Check | Fires on |
|---|---|
| `ST1000` | a package with no package comment, or one not of the form `Package name ...` |
| `ST1020` | an exported function whose comment does not start with its name |
| `ST1021` | an exported type whose comment does not start with its name |
| `ST1022` | an exported variable or constant whose comment does not start with its name |

golangci-lint keeps them off too. Its reference config documents the default as `["all", "-ST1000", "-ST1003", "-ST1016", "-ST1020", "-ST1021", "-ST1022"]`. The `.golangci.yml` in `golang-patterns` already overrides that with `checks: ["all"]`, so on a repo that copied it the four checks are **already on**. Verified with v2.13.1 on a package with three misnamed comments and no package comment: tool default, `0 issues`; `checks: ["all"]`, four findings:

```
wallet.go:1:1: ST1000: at least one file in a package should have a package comment
wallet.go:3:1: ST1021: comment on exported type Balance should be of the form "Balance ..." (with optional leading article)
wallet.go:8:1: ST1020: comment on exported function Add should be of the form "Add ..."
wallet.go:13:1: ST1022: comment on exported const MaxTransfer should be of the form "MaxTransfer ..."
```

The section of that file this skill depends on, named here so a narrowing edit does not lose the checks by accident:

```yaml
linters:
  settings:
    staticcheck:
      checks: ["all"]   # ST1000 and ST1020..ST1022 ride on "all". A project that trims this
                        # list keeps those four in; only -ST1003 / -ST1016 are up for debate.
  exclusions:
    generated: lax      # openapi.gen.go and the sqlc output carry the "Code generated" header
    # Never add `presets: [comments]`. It drops every doc-comment finding, from staticcheck
    # and from revive alike, with no warning. Verified: same four defects, 0 issues.
```

- **`revive` overlaps on purpose.** `golang-patterns` enables it, and its default rules `package-comments` and `exported` check the same thing. golangci-lint reports one finding per line (`issues.uniq-by-line`, default `true`), so a bad comment shows up once, under whichever linter ran first; with that option off the same file reports eight. Either message means the same fix.
- **Generated files are exempt through their header, not through a path rule.** `exclusions.generated: lax` recognizes `// Code generated ... DO NOT EDIT.`, which oapi-codegen and sqlc both emit. The oapi-codegen output declares `ChiServerOptions` and `MiddlewareFunc` with no comment at all and lints clean; nothing in it needs a `//nolint`.
- **The post-edit hook already runs these.** `~/.claude/hooks/go-post-edit.sh` runs `golangci-lint run` on the edited package with the repo's config, so with `checks: ["all"]` a misnamed comment is reported at edit time, before CI.
- **Read the result with `go doc`.** `go doc ./internal/wallets` lists the package comment and every exported symbol with its first sentence; `go doc ./internal/wallets Service.Transfer` prints one symbol in full. It is the fastest way to see what a caller sees and whether the summary sentence survives on its own.

## API contract: spec-first with oapi-codegen

The generator is settled; do not reopen it. `swaggo/swag` wants an annotation block above every handler, which is the Javadoc noise this team asked to avoid, and emits Swagger 2.0 only. `huma` is a framework: handlers are rewritten in its generic shape and errors come out as RFC 9457 problem documents, which collide with the envelope. `oapi-codegen` reads a YAML written by hand, has a dedicated `chi-server` generator, and asks nothing of an existing handler except that its signature match the generated interface. The contract is OpenAPI **3.0.x** (verified with 3.0.3).

### Layout

```
api/openapi.yaml             # the contract. Hand-written, reviewed in every PR that touches an endpoint.
api/oapi-codegen.yaml        # generator config
internal/api/doc.go          # package comment + the //go:generate directive
internal/api/openapi.gen.go  # generated: models, ServerInterface, the chi registrar. Never edited.
internal/api/wallets.go      # the resource that implements ServerInterface (chi-patterns shape)
```

`openapi.gen.go` lands next to the handlers because the config's `output` resolves relative to the directory `go generate` runs in, which is the package holding the directive (verified). The `.gen.go` suffix is a courtesy for humans; the lint exemption comes from the header.

### Pin the generator with the `tool` directive

```bash
go get -tool github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0
go get github.com/oapi-codegen/runtime@v1.7.0   # imported by the generated file
go generate ./...                               # runs the directive in internal/api/doc.go
```

```go
// internal/api/doc.go

// Package api holds the HTTP layer of the service: the response envelope, the
// resource structs, and the server interface generated from api/openapi.yaml,
// which is the contract every handler in this package implements.
//
//go:generate go tool oapi-codegen -config ../../api/oapi-codegen.yaml ../../api/openapi.yaml
package api
```

This departs from the `version-standards` rule that keeps sqlc as a `go install`, not a `require`, and the difference is deliberate. The generated file imports `github.com/oapi-codegen/runtime` for parameter binding, so a `require` exists regardless; adding the generator itself under a `tool` directive pins the exact version every laptop and the CI runner generate with, and `go tool oapi-codegen` needs no PATH setup (neither `oapi-codegen` nor `staticcheck` is installed as a binary in this workspace, and neither needs to be). The cost is real: `go get -tool` pulls roughly fifteen indirect requires (kin-openapi and its YAML stack) into `go.mod` and `go.sum`, and Dependabot will open PRs for that graph. **Prerequisite**: the `tool` directive needs `go 1.24` or later in `go.mod`. A module still on `go 1.22` must bump the directive first, and per `golang-patterns` that bump is the semantics risk event (loop variables), so it is its own reviewed change, not a side effect of adopting the generator.

CI keeps the file honest with one line after the build: `go generate ./... && git diff --exit-code -- internal/api/openapi.gen.go`. A PR that edits the YAML and forgets to regenerate fails there instead of shipping a contract the server does not implement.

### The generator config

```yaml
# api/oapi-codegen.yaml
package: api
output: openapi.gen.go        # relative to internal/api, where `go generate` runs
generate:
  models: true
  chi-server: true
output-options:
  exclude-tags: [public]      # public operations are documented but not generated: see the router delta
  skip-prune: true            # keep LoginRequest and friends even though no generated operation uses them
```

`exclude-tags` reads the operation's `tags`, so the public set is visible in the contract itself rather than only in the generator config. Without `skip-prune`, oapi-codegen drops every schema the excluded operations were the only users of, and the hand-written login handler loses its generated request type (verified: `LoginRequest` vanished, then came back).

### The contract, modeled on the envelope

The schemas describe the JSON that `WriteData` and `WriteError` already produce. No second error format: `error` is the `APIError` from `chi-patterns`, `meta` is its `Meta`, and both are `nullable` because the envelope renders all four keys on every response.

```yaml
# api/openapi.yaml (abridged: the GET/POST /wallets operations and the TokenResponse
# schema are omitted; the full contract is what the next section was generated from)
openapi: 3.0.3
info: { title: Centinela API, version: 1.0.0 }
security:
  - bearerAuth: []
paths:
  /login:
    post:
      operationId: login
      tags: [public]            # excluded from generation, registered by hand in the public group
      security: []
      requestBody:
        required: true
        content:
          application/json:
            schema: { $ref: "#/components/schemas/LoginRequest" }
      responses:
        "200": { description: Session token., content: { application/json: { schema: { $ref: "#/components/schemas/TokenResponse" } } } }
        "401": { $ref: "#/components/responses/Error" }
  /wallets/{walletID}:
    get:
      operationId: getWallet
      parameters:
        - { name: walletID, in: path, required: true, schema: { type: string, format: uuid } }
      responses:
        "200": { description: One wallet., content: { application/json: { schema: { $ref: "#/components/schemas/WalletResponse" } } } }
        "404": { $ref: "#/components/responses/Error" }
components:
  securitySchemes:
    bearerAuth: { type: http, scheme: bearer, bearerFormat: JWT }
  responses:
    Error:
      description: Failure envelope. Same shape on every status.
      content: { application/json: { schema: { $ref: "#/components/schemas/ErrorResponse" } } }
  schemas:
    APIError:
      type: object
      required: [code, message]
      properties:
        code: { type: string, example: ValidationError }
        message: { type: string }
        details: { type: array, items: { $ref: "#/components/schemas/FieldError" } }
    FieldError:
      type: object
      required: [field, rule]
      properties: { field: { type: string }, rule: { type: string }, param: { type: string } }
    Meta:
      type: object
      required: [total, page, limit]
      properties: { total: { type: integer }, page: { type: integer }, limit: { type: integer } }
    Wallet:
      type: object
      required: [id, currency, balanceCents]
      properties:
        id: { type: string, format: uuid }
        currency: { type: string, example: COP }
        balanceCents: { type: integer, format: int64 }
    CreateWalletRequest:
      type: object
      required: [currency]
      properties:
        currency:
          type: string
          minLength: 3
          maxLength: 3
          x-oapi-codegen-extra-tags: { validate: "required,len=3,uppercase" }   # the enforced rule
    LoginRequest:
      type: object
      required: [email, password]
      properties:
        email:
          type: string
          format: email
          x-go-type: string                                             # keep validator in charge
          x-oapi-codegen-extra-tags: { validate: "required,email" }
        password: { type: string, x-oapi-codegen-extra-tags: { validate: "required,min=8" } }
    WalletResponse:
      type: object
      required: [success, data, error, meta]
      properties:
        success: { type: boolean, enum: [true] }
        data: { $ref: "#/components/schemas/Wallet" }
        error: { type: object, nullable: true }
        meta: { type: object, nullable: true }
    ErrorResponse:
      type: object
      required: [success, data, error, meta]
      properties:
        success: { type: boolean, enum: [false] }
        data: { type: object, nullable: true }
        error: { $ref: "#/components/schemas/APIError" }
        meta: { type: object, nullable: true }
```

- **`x-oapi-codegen-extra-tags` is the bridge to `chi-patterns`.** It becomes a struct tag on the generated type, ``Currency string `json:"currency" validate:"required,len=3,uppercase"` `` (verified), so `decodeAndValidate` works on the generated request type unchanged, and the documented constraint (`minLength: 3`) sits on the same lines as the enforced one (`len=3`). The reviewer checks that the two agree; nothing else does.
- **`format:` on a body field bypasses validator.** `format: email` generates `openapi_types.Email`, whose `UnmarshalJSON` rejects `"nope"` while decoding, which `decodeAndValidate` reports as `400 BadRequest` with no `details` (verified). `uuid` and `date` go through the same runtime types and `date-time` through `time.Time`, same mechanism. A body field that must answer `422` with per-field details keeps the `format` for the contract and adds `x-go-type: string`, so the Go field is a plain string and the `validate` tag decides. Path and query parameters keep their typed form: a malformed one is a `400` about the URL, and `ParamError` below gives it `details`.
- **The response schemas exist for clients.** `WalletResponse` documents the wire shape for the frontend and for the contract diff; the server keeps writing `Envelope[T]` through `WriteData`. Do not return the generated response struct from a handler. The two describe the same JSON, and the YAML review is where drift between them is caught.

### What the generator emits, and what implements it

From the full contract the block above abridges, `openapi.gen.go` contains the models (`Wallet`, `CreateWalletRequest`, `LoginRequest`, `APIError`, `FieldError`, `Meta`), the parameter structs, the error types the wrapper raises (`RequiredParamError`, `InvalidParamFormatError`, and three more), and the server surface:

```go
type ServerInterface interface {
	// (GET /wallets)
	ListWallets(w http.ResponseWriter, r *http.Request, params ListWalletsParams)
	// (POST /wallets)
	CreateWallet(w http.ResponseWriter, r *http.Request)
	// (GET /wallets/{walletID})
	GetWallet(w http.ResponseWriter, r *http.Request, walletID openapi_types.UUID)
}

type ChiServerOptions struct {
	BaseURL          string
	BaseRouter       chi.Router
	Middlewares      []MiddlewareFunc
	ErrorHandlerFunc func(w http.ResponseWriter, r *http.Request, err error)
}

func HandlerWithOptions(si ServerInterface, options ChiServerOptions) http.Handler
```

Path and query parameters arrive bound and typed; the handler never calls `chi.URLParam`. The resource keeps the `chi-patterns` shape, and the one-line assertion turns a forgotten endpoint into a compile error:

```go
// internal/api/wallets.go

// WalletsResource serves the wallet operations of the contract.
type WalletsResource struct{ q dbstore.Querier }

// Compile-time proof that the contract is fully implemented: a missing or
// misnamed method fails here, not at runtime.
var _ ServerInterface = WalletsResource{}

// GetWallet returns one wallet by id. A malformed walletID never reaches this
// method: the generated wrapper hands it to ParamError instead.
func (rs WalletsResource) GetWallet(w http.ResponseWriter, r *http.Request, walletID openapi_types.UUID) {
	wallet, err := rs.get(r.Context(), walletID) // returns walletResponse, a separate type
	if err != nil {
		WriteAPIError(w, r, err)
		return
	}
	WriteData(w, http.StatusOK, wallet, nil)
}

// CreateWallet opens a wallet in the given currency. The request type is the
// generated one, so the body shape cannot drift from the contract.
func (rs WalletsResource) CreateWallet(w http.ResponseWriter, r *http.Request) {
	var in CreateWalletRequest
	if !decodeAndValidate(w, r, &in) {
		return // envelope already written
	}
	wallet, err := rs.create(r.Context(), in)
	if err != nil {
		WriteAPIError(w, r, err)
		return
	}
	WriteData(w, http.StatusCreated, wallet, nil)
}

// ListWallets returns the wallets of the authenticated user. Query parameters
// arrive already bound; the server still caps limit.
func (rs WalletsResource) ListWallets(w http.ResponseWriter, r *http.Request, params ListWalletsParams) {
	page, limit := paginationFrom(params.Page, params.Limit) // the server caps limit, not the client
	wallets, total, err := rs.list(r.Context(), page, limit)
	if err != nil {
		WriteAPIError(w, r, err)
		return
	}
	WriteData(w, http.StatusOK, wallets, &Meta{Total: total, Page: page, Limit: limit}) // the only non-nil meta
}
```

No annotation block above any of them: the documentation of `GET /wallets/{walletID}` is its entry in the YAML, and the doc comment says what the method does for a Go reader. Two things the generated signatures force: a `*http.Request` the body never touches is spelled `_` (revive's `unused-parameter`, on by default in the `golang-patterns` config, flags a named one; `ParamError` below shows it), and the interface fixes the method set, so the `Handle[In, Out]` adapter from `chi-patterns` is for hand-registered routes only.

### Mounting: a delta on `NewRouter`, not a new router

The generated registrar calls `r.Group` and `r.Get`/`r.Post` on whatever `BaseRouter` it is given, so handing it the authenticated group's router puts every generated route under that group's `Timeout` and `RequireAuth` (verified: `401` without a bearer, `200` with one). The public group stays hand-registered, with the operations the YAML tagged `public`:

```go
r.Group(func(r chi.Router) { // public, fresh stack, same path space
	r.Use(middleware.Timeout(60 * time.Second))
	r.Post("/login", auth.Login) // uses the generated LoginRequest; the path string is the one hand-typed thing
	r.Get("/healthz", health.Live)
	r.Get("/readyz", health.Ready)
})
r.Group(func(r chi.Router) { // authenticated: every generated route lands here
	r.Use(middleware.Timeout(60 * time.Second))
	r.Use(auth.RequireAuth)
	HandlerWithOptions(wallets, ChiServerOptions{BaseRouter: r, ErrorHandlerFunc: ParamError})
})
```

The residual drift is exactly one string per public operation: the verb and path in `r.Post("/login", ...)` must match the YAML by hand. Keep that set tiny (login, registration, a signed webhook), and everything else goes through the interface.

`ErrorHandlerFunc` is not optional. The generated default is `http.Error(w, err.Error(), http.StatusBadRequest)`: plain text, outside the envelope, with the binding error's wording leaking to the client (verified: `text/plain` on `/wallets/not-a-uuid`). The replacement maps the wrapper's error types onto `WriteError`:

```go
// ParamError translates the generated wrapper's binding errors into the
// envelope. Without it the wrapper answers with http.Error in plain text.
func ParamError(w http.ResponseWriter, _ *http.Request, err error) {
	var (
		required *RequiredParamError
		format   *InvalidParamFormatError
	)
	switch {
	case errors.As(err, &required):
		WriteError(w, http.StatusBadRequest, "BadRequest", "A required parameter is missing.",
			[]FieldError{{Field: required.ParamName, Rule: "required"}})
	case errors.As(err, &format):
		WriteError(w, http.StatusBadRequest, "BadRequest", "A parameter has an invalid format.",
			[]FieldError{{Field: format.ParamName, Rule: "format"}})
	default:
		WriteError(w, http.StatusBadRequest, "BadRequest", "Malformed request parameters.", nil)
	}
}
```

**The generated models replace the hand-written envelope types.** `chi-patterns` declares `APIError`, `FieldError`, and `Meta` in `internal/api/envelope.go`; the contract generates the same three names into the same package, so a repo following both skills gets `APIError redeclared in this block`. The adaptation is small and verified: `envelope.go` drops its three type declarations and keeps `Envelope[T]`, `WriteData`, `WriteError`, and `writeJSON`; `WriteError` stops using the positional literal (the generated field order is `Code, Details, Message`, and `Details` is a `*[]FieldError`) and sets `Details` only when there is something to say, which keeps the `omitempty` behavior of the contract; and `decodeAndValidate` takes the address of `fe.Param()`, because `FieldError.Param` is a `*string` (the YAML did not mark it required).

```go
// WriteError writes a failure envelope with a stable code and a human message.
func WriteError(w http.ResponseWriter, status int, code, msg string, details []FieldError) {
	e := &APIError{Code: code, Message: msg}
	if len(details) > 0 {
		e.Details = &details
	}
	writeJSON(w, status, Envelope[any]{Success: false, Error: e})
}
```

If the frontend wants the contract served rather than copied, publish the same file from the public group. `//go:embed` cannot reach `..`, so the embedding file has to sit next to the YAML in a small package under `api/`, not inside `internal/api`.

## One validation layer, decided

oapi-codegen ships an optional request validator (`github.com/oapi-codegen/nethttp-middleware`, `OapiRequestValidator`, backed by kin-openapi's `openapi3filter`) that checks every request against the spec before the handler runs. **This stack does not use it.** `chi-patterns` made go-playground/validator the request validation of the API: the rules live as struct tags, the failures come back as `422 ValidationError` with per-field `details`, and `decodeAndValidate` is the single place that produces them. Running the middleware as well would validate every body twice, against two rule sets that can disagree, and the second one answers in kin-openapi's own error format, outside the envelope. The YAML's `minLength`, `maximum`, and `format` are documentation for the client; `x-oapi-codegen-extra-tags` puts the enforced rule on the same line so the two are reviewed together, and the tag is what the server enforces.

The same decision settles authentication placement. oapi-codegen v2.8.0 can still emit `BearerAuthScopes` into the request context so a middleware can enforce the spec's `security` per operation, but only behind `compatibility.enable-auth-scopes-on-context`, which its own schema marks **deprecated** and whose doc says to use the request validation middleware instead. Neither applies here: `RequireAuth` stays on the authenticated group as `chi-patterns` placed it, and the contract's `security: []` on a public operation is documentation that the `public` tag turns into the hand-registration above.

## Anti-Patterns

- **`@param` / `@return` / `@throws` blocks, or `/** */` above a declaration.** They render as literal text in `go doc`, and ST1020 rejects the comment for not starting with the name.
- **`// This function ...`, `// Helper that ...`, `// Returns the ...`.** The first word is the identifier. ST1020/ST1021/ST1022 exist for exactly this.
- **A blank line between the comment and its declaration.** Detached comment, undocumented symbol.
- **A sentence on every unexported helper.** The wiki says non-trivial; a comment that restates the name is noise the linter will never ask for.
- **A package comment in every file of the package.** go/doc concatenates them. One file, usually `doc.go` when there is a `//go:generate` to keep with it.
- **`golint`, or `golint` in `linters.enable`.** Archived 2021; golangci-lint v2 errors on the name.
- **`exclusions.presets: [comments]`.** Silences ST1000/ST1020/ST1021/ST1022 and revive's `exported`/`package-comments` at once, with no warning.
- **Trimming `staticcheck.checks` from `["all"]` to a list that omits the four ST doc checks.** They are off by the tool's default, so omission is removal.
- **swag annotations (`// @Summary`, `// @Router`) or `swag init`.** The verbose per-handler block the team asked to avoid, and Swagger 2.0 only.
- **`huma`, or any framework that owns the handler signature and the error format.** It brings RFC 9457 and displaces the envelope.
- **Editing `openapi.gen.go`, or fixing a lint finding inside it.** The next `go generate` erases the edit; the file is excluded from lint by its header.
- **`@latest` for the generator, or an unpinned `go install` instead of the `tool` directive.** Two laptops generate two different files.
- **Keeping the hand-written `APIError` / `FieldError` / `Meta` next to the generated ones.** Same package, same names: `redeclared in this block`. The envelope helpers stay, the three type declarations go.
- **Committing a YAML change without regenerating.** The `git diff --exit-code` step in CI is there to catch it; do not disable it.
- **Embedding the generated `Unimplemented` in the resource struct.** It makes a forgotten endpoint compile and answer `501`, defeating `var _ ServerInterface = WalletsResource{}`.
- **The default `ErrorHandlerFunc`.** Plain-text `400` outside the envelope, with the binding error's text exposed.
- **`format: email` (or `uuid`, `date`) on a body field expected to fail with `422`.** The runtime type rejects at decode time as a `400` with no `details`; add `x-go-type: string` and let the tag decide.
- **Returning a generated `*Response` struct from a handler.** That type documents the wire shape for clients; the server writes `Envelope[T]` through `WriteData`.
- **`OapiRequestValidator` next to `decodeAndValidate`.** Two validations, two error formats.
- **`enable-auth-scopes-on-context` for auth.** Deprecated in v2.8.0; auth lives in the chi group.
- **`strict-server`.** It replaces `http.ResponseWriter` with generated response objects, so `WriteData` and the envelope helpers stop applying.

## Related Skills

- `chi-patterns` for the envelope and `WriteData` / `WriteError` / `WriteAPIError`, `decodeAndValidate` and the validator instance the generated tags feed, the two router groups the registrar lands in, and the `Handle[In, Out]` adapter that stays on hand-registered routes.
- `golang-patterns` for the rest of `.golangci.yml` (the `checks: ["all"]` line this skill depends on lives there), gofmt/goimports, package layout, the `go` directive bump that the `tool` directive requires, and the error idioms a doc comment names (`errors.Is`, sentinels, typed errors).
- `sqlc-patterns` for the other generated file on this stack, exempt from the doc checks through the same `Code generated` header, and for the `Querier` the resource above holds.
- `golang-bootstrap` for `/healthz` and `/readyz`, which stay in the public group and out of the contract, and for the composition root that constructs the resource handed to `HandlerWithOptions`.
- `golang-websocket` for the upgrade route that OpenAPI 3.0 cannot describe and that stays hand-registered in its own group.
- `golang-testing` for `httptest` through the router (the way the four verification cases behind this skill were written) and the `withTx` fixture a resource implementing `ServerInterface` takes unchanged.
- `version-standards` for the pinned numbers quoted here; oapi-codegen v2.8.0 and runtime v1.7.0 belong in its Go table once the project adopts them.
