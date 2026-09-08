---
name: golang-money
description: How a monetary amount is represented, added, stored and serialized on the Go side of the Backend_Centinela ledger (chi v5 + sqlc/pgx against PostgreSQL), implementing the design fixed in `nucleo-financiero-ledger.md` (how money is represented) at the place `frontera-modulos-nucleo-financiero.md` fixes for it (`internal/nucleo`, in `nucleo.go` next to the core's public interface): integer minor units in a `bigint`, no float, double, real or NUMERIC anywhere including the API, conversion to major units only at presentation. Covers the `nucleo.Centavos` named type over `int64` with its two separate methods (`Int64` for logic, `PresentarUnidadesMayores` for a human, returning text), overflow-checked `Sumar`/`Restar` because Go wraps `int64` silently, why no sqlc override maps a money column to `Centavos` (sqlc's output lives in `internal/nucleo/internal/repositorio`, which `nucleo` imports for `*repositorio.Queries`, so an override pointing back at `nucleo` is an import cycle, reproduced), which leaves every generated money field as a raw `int64` (`pgtype.Int8` when nullable) that `nucleo` wraps as `Centavos(fila.Monto)` on the line after each `Queries` call and never lets out of the package unwrapped, the `::bigint` cast a `SUM(monto)` balance needs, and the JSON contract that closes the question the ledger document left open: amounts travel as strings (`"1000000"`), never as JSON numbers, because a full `int64` does not fit in the 2^53 range that RFC 8259 and JavaScript agree on and the Flutter client can compile to JavaScript. Activate when any field, column, parameter, DTO member, query result or JSON value represents money: an amount, a balance, a fee, a limit, a posting `monto`, a reconciliation figure. Not for named types, error wrapping or Go idioms in general (see `golang-patterns`), not for sqlc overrides, queries, the pool or Goose in general (see `sqlc-patterns`), not for the response envelope the amount travels inside (see `chi-patterns`), not for boot or config (see `golang-bootstrap`), and not for tests (see `golang-testing`).
---

# Go Money

One type, one parser, two arithmetic methods, one wire shape. Money on the Go side of the ledger is `nucleo.Centavos`, a named `int64` holding minor units, and nothing else: not a `float64`, not a `pgtype.Numeric`, not a bare `int64` that could be an id. The one place a bare `int64` holds money is the sqlc-generated repository, for exactly as long as it takes `nucleo` to wrap it on the line after the query; the sqlc section says why that is so and what it costs.

The design is not argued here. `proyecto-fin-de-grado/nucleo-financiero-ledger.md` fixes it: chapter 6 ("Cómo se representa el dinero") bans every floating-point type at every point of the system, API responses and JSON included, stores `$10.000` as `1000000` in a `bigint`, and allows the conversion to pesos only in the presentation layer, in the last step before a person reads the number; chapter 4 gives the schema this skill maps (`posting.monto bigint NOT NULL CHECK (monto <> 0)`, ids as `bigint GENERATED ALWAYS AS IDENTITY`); chapter 2 gives the sign (positive is a debit, negative a credit, so a user's balance is negative in the ledger and the app inverts it). This skill translates those decisions into Go and closes the one point that document leaves open, "los montos viajan como enteros o como cadenas": they travel as strings.

Where the type lives is fixed by a second, separate document of the same thesis project, `proyecto-fin-de-grado/frontera-modulos-nucleo-financiero.md`, chapter 5 ("La opción A en código"): "El núcleo financiero completo es un paquete Go, `internal/nucleo`, con un archivo por bloque adentro, una sola frontera transaccional, un solo dueño de tablas y una sola interfaz pública." In its folder tree `nucleo.go` is "interfaz pública y tipos", and its public interface opens with `// Centavos: entero, nunca flotante (cap. 6).` followed by `type Centavos int64`, before `LectorNivelKYC`, `Riel` and `Servicio`. The two documents are not one document with shared chapters: the ledger document says how money is represented, the boundaries document says which Go package and file it belongs to, and a chapter number in this skill refers to the ledger document unless it says otherwise.

Verified 2026-09-04 against go1.27.0, sqlc **v1.31.1** generating `pgx/v5` code from the ledger DDL, and pgx **v5.10.0**. The first pass ran pgx against a PostgreSQL 16 container under an override design this skill no longer has (generated rows typed as `Centavos`). Of what it exercised, four facts stand and are restated here, not re-run: pgx scans a `bigint` past 2^53 exactly (9007199254740993 round-tripped), Postgres types `SUM(bigint)` as `numeric` and a `::bigint` cast makes sqlc emit `(int64, error)` for it, that cast raises `SQLSTATE 22003` when the sum does not fit, and `bigint + bigint` past the range raises the same. The scan of `bigint` into `Centavos` and the JSON shape of a generated row carrying `Centavos` were true of that design and are no longer claimed anywhere below. The JavaScript, RFC 8259, Dart and Stripe facts were checked against their official documentation before this skill was written and are restated, not re-derived. Same date, three later passes. The first two changed names and location only: the identifiers went from English (`money.Cents`, `Add`/`Sub`, `ParseCents`, `DisplayMajorUnits`, `ErrOverflow`/`ErrInvalid`) to the Spanish names below, following the repo convention of Spanish names unless a name is bound to tooling (`MarshalJSON`, `UnmarshalJSON` and `Int64` kept theirs because they implement or mirror the standard library); then the type moved from a money package of its own into `internal/nucleo/nucleo.go`, following chapter 5 of the boundaries document, with `Centavos` and both sentinel errors keeping their names, only the message prefix changing to `nucleo:`, and the test file becoming `nucleo_test.go`.

The third pass changed behavior on the sqlc side, and its results are the ones this skill states. A three-package probe was built under one module: `internal/nucleo` importing `internal/nucleo/internal/repositorio` for `*repositorio.Queries`, the latter generated by sqlc v1.31.1 (the `sqlc/sqlc:1.31.1` image) from the ledger DDL with `emit_json_tags: true` and no `overrides`, and nothing importing in the other direction. `go build ./...` passes; the generated `Posting` has `Monto int64`, `ReglaLimite.MontoMaximo` and `Descuadre.MontoInterno`/`MontoExterno` are `pgtype.Int8`, `InsertPostingParams.Monto` is `int64`, and `SaldoCuenta` with the cast is `(int64, error)`. The same tree regenerated with `column: "posting.monto"` pointing at `nucleo.Centavos` makes sqlc write `import ".../internal/nucleo"` into `models.go` and `go build ./...` stops with the `import cycle not allowed` message quoted verbatim in the sqlc section. `Saldo`, the insert params and the nullable helper were run through the generated `Queries` with a fake `DBTX` (no database), the reflect test over `Servicio` was checked to fail on an injected leak before being trusted, and `gofmt -l .`, `go vet ./...` and `go test -race -count=1 ./...` were clean on the whole tree, the JSON, arithmetic and `PresentarUnidadesMayores` cases re-running unchanged because the type did not change. One directive detail: pgx v5.10.0 requires `go 1.25.0`, so `go get` raised the probe's directive from `1.22` to `1.25.0`. `Centavos` alone, without pgx, still compiles under `go 1.22`, the directive the repo carries today; the repository package will not once pgx enters. `Servicio` beyond its `q *repositorio.Queries` field, `Transferir`, `Conciliar` and `DespacharOutbox` were not reproduced and nothing about them is claimed here.

## Role and boundaries (which skill to use)

This skill = the `Centavos` type, its two operations, its sqlc mapping and its wire shape. `Centavos` shares `internal/nucleo/nucleo.go` with the rest of the core's public interface (`Servicio` and its `Transferir`, `Saldo`, `Conciliar` and `DespacharOutbox`; the `LectorNivelKYC` and `Riel` interfaces), because chapter 5 of the boundaries document puts "interfaz pública y tipos" in that one file. Sharing the file does not widen this skill: it documents the money type and nothing else in that file. Everything around it already has an owner:

- **`Servicio`, the orchestration inside `Transferir` and the one transaction it opens, `Saldo`, `Conciliar`, `DespacharOutbox`, the `LectorNivelKYC` and `Riel` interfaces, and the package doc comment of `nucleo`** → the core ledger service, to be documented where the core is documented when it is written. Not here, even though they sit in the same file as `Centavos`; this skill says that `Saldo` returns a `Centavos` and shows the one line inside it, and inside every other method that reads or writes a money column, where the `int64` that `repositorio` returns becomes one. The rest of each method is the core's.
- **Named types, sentinel errors, `%w`, `errors.Is`** → `golang-patterns`. This skill only says which named type money is and which two operations it has.
- **`sqlc.yaml` in general, the generate workflow, the `Querier`, transactions, Goose, pagination** → `sqlc-patterns`. Here: why money columns have no override entry at all, the cast an aggregate needs, and the wrap `nucleo` does on the line after each `Queries` call.
- **The `{success, data, error, meta}` envelope, `decodeAndValidate`, DTO separation** → `chi-patterns`. Here: what the amount field inside `data` looks like, why a client-sent JSON number is a 400, and one obligation that skill's DTO rule now carries for money: a generated row's `Monto` is a raw `int64` that marshals as a JSON number, so the row-to-DTO mapping at the service boundary is where the string contract is enforced, not a nicety.
- **The composition root and config** → `golang-bootstrap`. No amount is ever read from the environment.
- **Tests** → `golang-testing`. The cases that pin this type are listed below as the minimum.
- **Why `bigint` and not `NUMERIC`, why no balance column, the double-entry invariant, the deferred trigger** → the ledger document itself. Do not restate its argument in code comments; cite the chapter.

## When to Activate

- Declaring a struct field, a parameter, a DTO member or a map value that holds an amount, a balance, a fee, a commission, a limit, or the difference between two amounts.
- Writing a migration that adds a money column, or the `Servicio` method that reads or writes it: the wrap to `Centavos` happens there, because `sqlc.yaml` has no override for it.
- Any `+`, `-`, comparison or `SUM` over amounts, in Go or in a query result that lands in Go.
- Any JSON, OpenAPI schema, notification text, receipt or export where an amount leaves the process.
- Reviewing code that mentions `float64`, `NUMERIC`, `pgtype.Numeric`, `strconv.ParseFloat`, `/ 100`, `* 100` or `math.Round` anywhere near money.

The skeleton in `Backend_Centinela` predates the ledger document and is the concrete instance of everything below, not a pattern to copy: `migrations/0002_create_wallets.sql:1` (`balance NUMERIC(14,2)`, a stored balance column on top of the type), `migrations/0003_create_transactions.sql:1` (`amount NUMERIC(14,2)`), `internal/wallets/domain.go:3` (`Balance float64`), `internal/transactions/domain.go:3` (`Amount float64`), `internal/wallets/repository.go:4` (`Balance(...) (float64, error)`), `internal/transactions/service.go:10` (`balance<req.Amount` on two floats). Each one is replaced by what follows.

## The `Centavos` type (`internal/nucleo/nucleo.go`)

`nucleo.go` is "interfaz pública y tipos" of the core (boundaries document, chapter 5): `Centavos` and its methods live there, next to `Servicio`, `LectorNivelKYC` and `Riel`, not in a file dedicated to money. The extract below is the money part of that file and nothing else; the package doc comment of `nucleo` describes the core, so it is not written here.

```go
package nucleo

// The money part of nucleo.go. Servicio, LectorNivelKYC and Riel share this file and belong
// to the core ledger service. A monetary amount is integer minor units in a Centavos: never a
// float, never a decimal, never a division by 100 outside PresentarUnidadesMayores.

// Centavos is an amount in minor units of the ledger's single currency: $10.000 is
// Centavos(1000000). Positive is a debit, negative a credit, exactly as posting.monto stores
// it. It is a named int64, so the compiler refuses an untyped int64 (an id, a count) where
// money is expected, and every arithmetic on it goes through Sumar and Restar, never through
// the bare operators.
type Centavos int64

// ErrDesbordamiento reports a Sumar or Restar whose exact result does not fit in int64.
var ErrDesbordamiento = errors.New("nucleo: desbordamiento de int64")

// ErrMontoInvalido reports text that is not an integer number of minor units.
var ErrMontoInvalido = errors.New("nucleo: el monto debe ser un número entero de unidades menores")

// Int64 returns the raw minor units: the value for every rule, comparison, query parameter
// and posting. The ledger never sees anything else. The name stays in English on purpose: it
// names the Go predeclared type it returns, not domain vocabulary, exactly as (*big.Int).Int64
// does in the standard library.
func (c Centavos) Int64() int64 { return int64(c) }

// PresentarUnidadesMayores renders the amount in major units ("10000.00", "-0.05") for a
// human to read: a notification, a receipt, an admin export. It returns text, never a
// float64, and it is the only place on this side of the system where the minor unit is
// converted. Nothing parses this string back into an amount.
func (c Centavos) PresentarUnidadesMayores() string {
	s := strconv.FormatInt(int64(c), 10) // no negation: -math.MinInt64 would wrap
	sign := ""
	if strings.HasPrefix(s, "-") {
		sign, s = "-", s[1:]
	}
	for len(s) < 3 { // "5" becomes "005" so the split below yields "0.05"
		s = "0" + s
	}
	return sign + s[:len(s)-2] + "." + s[len(s)-2:]
}

// ParsearCentavos is the single parser for an amount that arrives as text: a JSON string, a
// query parameter, a CSV cell. It accepts an optional sign and decimal digits, nothing else:
// "12.50", "1e3", " 5" and anything past int64 are all ErrMontoInvalido.
func ParsearCentavos(s string) (Centavos, error) {
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, ErrMontoInvalido // the offending text is not echoed: it may be 1 MiB of client input
	}
	return Centavos(v), nil
}
```

- **A named type, not an alias.** `type Centavos = int64` would be an alias: no methods, and an id assignable where money is expected. `type Centavos int64` gives the compiler the distinction and the type its methods. The two money libraries reviewed for this decision (`Rhymond/go-money`, `gocanto/money`) wrap the `int64` in a struct because they also carry a currency; the ledger is single-currency by its own scope (chapter 5, "Multimoneda", deferred), so the struct is not earned yet. When a second currency arrives, the change is a struct holding a `Centavos` and a currency code, not a rewrite of this type.
- **Two methods, two audiences, and the name says which.** `Int64()` is the value for logic. `PresentarUnidadesMayores()` exists to show a number to a person and for nothing else: the ledger's rule is that the conversion to pesos happens once, at presentation. The method returns a `string` built by splitting decimal digits, so no `float64` ever exists in the process and `math.MinInt64` renders without the negation that would wrap. Comparing, sorting or summing on its output is a bug by construction.
- **There is no constructor from major units.** Not `DesdePesos(float64)`, not `DesdeUnidadesMayores(string)`. An amount enters the process as minor units, from the database (as the `int64` sqlc generates, wrapped in `nucleo` as the sqlc section shows) or from `ParsearCentavos`, and a literal is written as minor units: `nucleo.Centavos(1000000)` from another package, `Centavos(1000000)` inside `nucleo`.
- **The sign travels unchanged.** A user's `disponible` balance is a liability and reads negative from `SUM(monto)` (chapter 2); `PresentarUnidadesMayores` keeps that sign. Inverting it for the screen is the presentation layer's decision, taken where it renders, never inside this type.

## Arithmetic that refuses to wrap

Go integer overflow does not panic: it wraps silently (go.dev/ref/spec, "Integer overflow"), and `math/bits.Add64`/`Sub64` only exist for `uint64`. There is no standard-library helper for a checked signed addition, so the check is written by hand, before the operation, and it lives on the type so nobody writes it twice.

```go
// Sumar returns c + o, or ErrDesbordamiento when the exact sum does not fit. Go wraps int64
// silently (go.dev/ref/spec, "Integer overflow") and math/bits only covers uint64, so the
// check is written by hand, before the addition.
func (c Centavos) Sumar(o Centavos) (Centavos, error) {
	if (o > 0 && c > math.MaxInt64-o) || (o < 0 && c < math.MinInt64-o) {
		return 0, fmt.Errorf("sumar %d + %d: %w", c, o, ErrDesbordamiento)
	}
	return c + o, nil
}

// Restar returns c - o with the same guarantee. It is not c.Sumar(-o): negating math.MinInt64
// wraps before Sumar could look at it, which is also why a negation is written
// Centavos(0).Restar(c).
func (c Centavos) Restar(o Centavos) (Centavos, error) {
	if (o < 0 && c > math.MaxInt64+o) || (o > 0 && c < math.MinInt64+o) {
		return 0, fmt.Errorf("restar %d - %d: %w", c, o, ErrDesbordamiento)
	}
	return c - o, nil
}
```

- **Every `+` and `-` on `Centavos` goes through `Sumar` and `Restar`.** Bare operators on two `Centavos` compile, which is exactly the problem: the review rule is that they do not appear. Comparisons (`<`, `>=`, `==`) cannot overflow and stay as operators.
- **Negation is `Centavos(0).Restar(c)`.** `-c` wraps on `math.MinInt64`, and `c.Sumar(-o)` has already wrapped before `Sumar` runs. `Restar` reports that one case (verified: `Centavos(0).Restar(math.MinInt64)` returns `ErrDesbordamiento`).
- **This is the whole library.** Two methods. Summing the postings of an entry to assert zero before the insert, computing a balance as a corte plus later postings (chapter 7) in Go, checking a limit as `saldo.Restar(monto)`: all of them are chains of `Sumar`/`Restar`. A fee or a tax that needs a percentage brings a rounding rule with it, and the ledger document has not taken that rule yet (GMF waits on a DIAN primary source, chapter 11), so a `Multiplicar`, `Porcentaje` or `Repartir` is its own reviewed change with the rounding written down, never something added on the way to a feature.
- **Go is the weak point, not Postgres.** The engine raises `SQLSTATE 22003 numeric_value_out_of_range` on `bigint + bigint` past the range, and `SUM(bigint)` returns `numeric`, exact at any size (both verified against PostgreSQL 16). The silent wrap only exists on this side.

## sqlc: no override for money, the wrap happens in `nucleo`

`sqlc-patterns` owns `sqlc.yaml`; this section says what its `overrides` list does not contain, why, and what `nucleo` does instead. The short version: sqlc maps no money column to `Centavos`, every one of them comes out as the default Go type for a `bigint`, and that `int64` becomes a `Centavos` on the line after the `Queries` call, inside the `Servicio` method that made it.

**Why there is no override: the import cycle.** The boundaries document puts sqlc's output in `internal/nucleo/internal/repositorio` (chapter 5, "Las carpetas") and gives `Servicio` a `q *repositorio.Queries` field (chapter 5, "La interfaz pública"), so `nucleo` imports `repositorio`. An override such as `column: "posting.monto"` with `go_type: ".../internal/nucleo.Centavos"` makes sqlc write `import ".../internal/nucleo"` at the top of the generated `models.go`, and Go refuses the pair. Reproduced 2026-09-04 with a three-package probe under go1.27.0, `repositorio` generated by sqlc v1.31.1 with that override in place (the probe's module path differs from the repo's; the shape does not):

```text
package probe.local/centinela/internal/nucleo
	imports probe.local/centinela/internal/nucleo/internal/repositorio from nucleo.go
	imports probe.local/centinela/internal/nucleo from models.go: import cycle not allowed
```

The same tree builds when the override is absent. Where `Centavos` lives is fixed by chapter 5 and does not move; the overrides go. That is the decision, taken with the cost below in view, and this skill implements it instead of routing around it.

```yaml
# sqlc.yaml, under gen.go (the rest of the file is in `sqlc-patterns`).
# There is no `overrides` entry for any money column. posting.monto, transferencia.monto,
# saldo_corte.saldo, extracto_custodio.saldo, regla_limite.monto_maximo,
# descuadre.monto_interno and descuadre.monto_externo all keep sqlc's default Go type, on
# purpose: an entry pointing any of them at internal/nucleo.Centavos is the import cycle
# above. The wrap to Centavos is written in nucleo, next to each Queries call.
```

What sqlc v1.31.1 generates from the ledger DDL with no override (verified this pass, `emit_json_tags: true` as in `sqlc-patterns`, `emit_pointers_for_null_types` not set):

```go
type Posting struct {
	ID       int64  `json:"id"`
	EntryID  int64  `json:"entry_id"`
	CuentaID string `json:"cuenta_id"`
	Monto    int64  `json:"monto"` // raw: the same Go type as ID and EntryID until nucleo wraps it
	Moneda   string `json:"moneda"`
}
type InsertPostingParams struct { EntryID int64; CuentaID string; Monto int64; Moneda string }
func (q *Queries) GetPosting(ctx context.Context, id int64) (Posting, error)
func (q *Queries) SaldoCuenta(ctx context.Context, cuentaID string) (int64, error) // with the ::bigint cast
// SaldoCorte.Saldo, ExtractoCustodio.Saldo, Transferencium.Monto: int64
// ReglaLimite.MontoMaximo, Descuadre.MontoInterno, Descuadre.MontoExterno: pgtype.Int8
//   (*int64 instead if sqlc.yaml ever sets emit_pointers_for_null_types: true)
```

(`Transferencium` is sqlc's own singular of `transferencia`; the name is `sqlc-patterns`' business, not money's.)

**The cost, in plain words.** Inside `repositorio`, an id and an amount are the same Go type. `fila.ID`, `fila.EntryID` and `fila.Monto` are three `int64`s; `q.GetPosting(ctx, fila.Monto)` compiles, `InsertPostingParams{Monto: fila.ID}` compiles, and nothing in the compiler tells them apart until `nucleo` wraps the amount. That is exactly the confusion `Centavos` exists to prevent, and the first design of this skill prevented it at generation time. Now, for the width of the generated package and the one line after each call, the protection is not the compiler's but the discipline of wrapping at the edge. It is the same discipline the balance query always needed, because a column override never could target `SUM(monto)::bigint`; what changed is that the exception became the rule for every money column, computed or stored. Outside `repositorio` nothing is lost: from the wrap onward, `Centavos` is as strict as before.

**The wrap, in code.** The `int64` becomes a `Centavos` on the line after the `Queries` call and before it touches anything else. `Saldo` is the one method whose signature chapter 5 fixes (`Saldo(ctx context.Context, cuentaID string) (Centavos, error)`); the other shapes are the ones the rest of `nucleo` repeats. All of them compiled and ran this pass through the generated `Queries` with a fake `DBTX`; the last two are the body fragments that matter, not whole functions:

```go
// Saldo returns the balance of one account. SaldoCuenta is generated as (int64, error)
// because of the ::bigint cast; the int64 becomes Centavos here, on the line after the call.
func (s *Servicio) Saldo(ctx context.Context, cuentaID string) (Centavos, error) {
	saldo, err := s.q.SaldoCuenta(ctx, cuentaID)
	if err != nil {
		return 0, err
	}
	return Centavos(saldo), nil
}

// montoDeFila is the one place a posting row's amount becomes money. It takes the row, not
// the int64, so the call site cannot hand it fila.ID by mistake.
func montoDeFila(fila repositorio.Posting) Centavos {
	return Centavos(fila.Monto)
}

// centavosOpcional wraps a nullable money column. NULL stays nil: absent money is not zero
// money. With emit_pointers_for_null_types the parameter is *int64 and the test is p == nil.
func centavosOpcional(v pgtype.Int8) *Centavos {
	if !v.Valid {
		return nil
	}
	c := Centavos(v.Int64)
	return &c
}

// The params side is the other half of the wrap: InsertPostingParams.Monto is int64, so a
// Centavos only fits through Int64(). The row that comes back is wrapped like any other.
fila, err := s.q.InsertPosting(ctx, repositorio.InsertPostingParams{
	EntryID: entryID, CuentaID: cuentaID, Monto: monto.Int64(), Moneda: moneda,
})
if err != nil {
	return 0, err
}
return montoDeFila(fila), nil

// Summing the lines of an entry: each raw Monto is wrapped, and only Sumar touches it after.
var total Centavos
for _, fila := range filas {
	if total, err = total.Sumar(montoDeFila(fila)); err != nil {
		return 0, err
	}
}
```

- **The trap, concretely, now at the wrap site.** `Centavos(fila.ID)` compiles exactly as well as `Centavos(fila.Monto)`: the conversion is per field, chosen by column name, and the compiler does not check the choice. The first design's trap (`db_type: "pg_catalog.int8"` turning `journal_entry.id`, `posting.id`, `posting.entry_id`, `outbox.id`, `descuadre.id` and `saldo_corte.hasta_posting_id` into `Centavos`, confirmed then by generating with it) is gone because there is no override at all; what replaced it is a conversion a human picks. So the wrap takes the row and names the column (`montoDeFila(fila)`), never a loose `int64` a caller could fill with an id, and a review of `nucleo` reads every `Centavos(` conversion against the column it names.
- **A migration that adds a money column changes nothing in `sqlc.yaml`.** The column comes out as `int64` (or `pgtype.Int8`) like every other `bigint`, so there is no override to add or to forget. What the same change must add is the wrap in the `Servicio` method that reads or writes the column; `sqlc generate && git diff --exit-code` in CI (`sqlc-patterns`) does not notice a missing wrap any more than it noticed a missing override. The review checklist is now: every `Queries` call that returns a money column is followed by `Centavos(...)` or `centavosOpcional(...)` before the value is used, and every `Params` money field is filled through `.Int64()`.
- **`SUM(monto)` needs `::bigint`, and is no longer the odd one out.** `SELECT COALESCE(SUM(monto), 0) AS saldo` is the balance query of chapter 7, Postgres types `SUM(bigint)` as `numeric`, so sqlc generates `(interface{}, error)` for it, which pgx fills with a `pgtype.Numeric` at runtime (pass-1 fact). That is the door to `.Float64()` on the single most important query. Write `COALESCE(SUM(monto), 0)::bigint AS saldo`, get `(int64, error)` (verified this pass), and wrap it in `Saldo` as above. If a real balance ever exceeds `int64`, the cast raises `22003` (pass-1 fact) instead of returning a wrong number, which is the right failure for a ledger.
- **pgx scans sqlc's default types, and `Centavos` never meets pgx.** `bigint` lands in an `int64` and NULL in a `pgtype.Int8` with `Valid == false`, both sqlc's stock path; values past 2^53 stay exact (9007199254740993 round-tripped in pass 1). On the way in, a `Centavos` reaches the driver as the `int64` from `Int64()`; on the way out, the wrap happens after the scan. So no `Scanner`/`Valuer` is needed on the type, for a simpler reason than before: the driver never sees it, and adding one is dead code to maintain.
- **A generated row does not serialize correctly, and this is the claim that turned false.** With `emit_json_tags: true`, `repositorio.Posting{ID: 1, EntryID: 1, CuentaID: "activo:custodia:banco", Monto: 9007199254740993, Moneda: "COP"}` marshals as `{"id":1,"entry_id":1,"cuenta_id":"activo:custodia:banco","monto":9007199254740993,"moneda":"COP"}` (verified this pass): the amount is a JSON number, already past 2^53, exactly what the contract below forbids. The same row through `montoDeFila` into a `nucleo` type marshals `"monto":"9007199254740993"` (verified). The `sqlc-patterns` and `chi-patterns` rule that rows are mapped to a DTO at the service boundary is therefore no longer good hygiene for money: it is the only place the string contract is enforced. A `repositorio` row never reaches `json.Marshal`, `WriteData` or a template; it is wrapped first, every time.
- **The structural net, and the test that pins it.** Go's `internal/` rule means only code under `internal/nucleo` can name a `repositorio` type, but a caller can still receive one through an exported method and marshal it. Chapter 5 already fixes that "ningún tipo de `pgx` aparece en ninguna firma" of `Servicio`; the same rule extended to `repositorio` types is what makes the wrap unavoidable: every exported method of `Servicio` returns `nucleo` types only (`Centavos`, `Transferencia`), so a raw `int64` amount cannot leave the package. The reflect test in the minimum tests below pins it and was checked to fail on an injected `FilaCruda() (repositorio.Posting, error)` before being trusted. It does not catch a `nucleo` type that declares a money field as `int64` by mistake; that is the second review rule: every money field on a `nucleo` type is `Centavos` or `*Centavos`, never `int64`.

## JSON: an amount is a string

```go
// MarshalJSON emits the amount as a JSON string ("1000000"), never as a JSON number. Because
// it hangs off the type, a Centavos inside any struct that reaches the encoder (a response
// DTO, a notification payload) cannot leave the process as a number. A generated repositorio
// row does not carry Centavos: its Monto is a raw int64 and would marshal as a number, which
// is why a row is wrapped into a nucleo type before anything encodes it.
func (c Centavos) MarshalJSON() ([]byte, error) {
	b := make([]byte, 0, 22) // quote + sign + 19 digits + quote
	b = append(b, '"')
	b = strconv.AppendInt(b, int64(c), 10)
	return append(b, '"'), nil
}

// UnmarshalJSON accepts exactly one shape: a JSON string of integer minor units. A JSON
// number is rejected even when it would fit, because a client that sends one holds the
// amount as a number, which is what the contract forbids. JSON null is rejected too: absent
// money is not zero money. An optional amount is a *Centavos, and for a null the decoder sets
// the pointer to nil without ever calling this method.
func (c *Centavos) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return ErrMontoInvalido // a JSON number, bool, object or array
	}
	// json.Unmarshal treats null as a no-op and leaves s empty, so null is rejected here too.
	v, err := ParsearCentavos(s)
	if err != nil {
		return err
	}
	*c = v
	return nil
}
```

On the wire, inside the envelope from `chi-patterns`, a balance reads:

```json
{"success": true, "data": {"cuenta_id": "pasivo:usuario:a3f1:disponible", "saldo": "-1000000"}, "error": null, "meta": null}
```

Negative because it is a liability (chapter 2); the client inverts it for the screen and divides for display, and does neither anywhere else.

**Why a string, and why this is not about today's balances.** The ledger document left the choice open between integers and strings. The facts that close it:

- JavaScript's `Number.MAX_SAFE_INTEGER` is 2^53 - 1 (9.007.199.254.740.991), and RFC 8259 section 6 recommends that same range for interoperable JSON numbers in general, not only for JavaScript consumers. Past it, a JSON number is parsed inexactly by any conforming implementation that uses IEEE 754 doubles.
- The range the document computes for `bigint`, about 9.2 x 10^18, exceeds that limit by a factor of roughly 1024. An `int64` amount as a JSON number is therefore a contract that is only correct for a subset of its own type.
- The real client is a Flutter app. Dart's official documentation states that `int` is a true 64-bit integer on native platforms, and that under `dart2js` it becomes a JavaScript `double` with precision lost past 53 bits. It does not distinguish `dart2wasm` on this point, and Flutter keeps a JavaScript fallback when the browser lacks WasmGC, so the web build cannot be assumed to be safe.
- Stripe, the usual precedent for "just send an integer", does exactly that and also documents and enforces a ceiling of 8 digits per amount field, far below 2^53. This project has no such enforced ceiling.
- The deciding reason is not magnitude. No balance is anywhere near 10^16 today. A financial system's transport format should not rest on "we are fine because the numbers are small": a full `int64` fits exactly in a string on every client that exists or will exist, without renegotiating the contract the day a reconciliation figure, a custody total or a test fixture crosses the line.

**The inbound sub-decision: string only, no tolerance for numbers.** The one choice this skill takes on its own is whether `UnmarshalJSON` also accepts a JSON number "for convenience in development". It does not, for the same reason the outbound side is a string: accepting numbers reintroduces the "numbers are small today" dependency on the inbound side, and it would pass every test with small fixtures. A client that sends a number holds the amount as a number, which is the situation the contract exists to make impossible, so it fails at the boundary, loudly, on the first request. `null` on a value field is rejected as well (absent money is not zero money); an optional amount is declared `*Centavos`, where the decoder never calls the method and `null` becomes `nil`. All of this is verified: number, `null`, `""`, `"12.50"`, `"1e3"`, `" 5"`, `true`, `{}` and a value past `int64` are each rejected with `nucleo.ErrMontoInvalido`; `"9007199254740993"` round-trips exactly; `*Centavos` takes `null` and a missing key as `nil`.

- **`UnmarshalJSON` is the one pointer receiver on `Centavos`, on purpose.** `golang-patterns` says never to mix receiver kinds on a type, and every other method here takes a value. `json.Unmarshaler` has to mutate, so its receiver must be a pointer; `time.Time` follows exactly this split (`MarshalJSON` on the value, `UnmarshalJSON` on the pointer). It is the deliberate exception, not an oversight to "fix" by moving the rest to pointers.
- **Through `decodeAndValidate` (`chi-patterns`) the error surfaces unchanged**: `errors.Is(err, nucleo.ErrMontoInvalido)` holds (verified), and it lands on the generic 400 branch because it is neither a `*json.UnmarshalTypeError` nor a `*http.MaxBytesError`. That is the correct status: the body is not a valid request, and the message stays fixed.
- **Query parameters and form values** go through the same parser: `nucleo.ParsearCentavos(r.URL.Query().Get("monto"))`. One parser, one set of rejections.
- **The client's side of the contract, in one line**: parse the string into an arbitrary-precision integer (`BigInt.parse` in Dart) or keep it as text for display; never `int.parse` on a web build.
- **In `api/openapi.yaml`** (the spec-first flow in `golang-docs`) the schema is `type: string` with `pattern: '^-?[0-9]+$'`, mapped to `nucleo.Centavos` through oapi-codegen's `x-go-type` and `x-go-type-import` extensions. Not exercised for this skill; `integer` with `format: int64` is the shape that must not appear there.

## Minimum tests (`golang-testing`)

The table cases that pin this type, in `internal/nucleo/nucleo_test.go` (`package nucleo`, the test file of `nucleo.go`; `golang-testing` fixes no file-name convention beyond the `_test.go` suffix, so the test file follows the source file it tests), map-keyed as `golang-testing` prescribes:

- `PresentarUnidadesMayores`: `0`, `5`, `-5`, `100`, `1000000`, `-1000000`, `math.MaxInt64`, `math.MinInt64` (the last one is the negation trap).
- `Sumar`/`Restar`: `MaxInt64 + 1`, `MinInt64 - 1`, `MinInt64 + (-1)`, `MaxInt64 - (-1)` and `0 - MinInt64` all return `ErrDesbordamiento`; the values one step inside the edge succeed.
- A one-line test that `var x int64 = math.MaxInt64; x++` equals `math.MinInt64`, so the reason the methods exist is in the suite, not in a comment.
- JSON: marshal of `0`, `-1000000`, `MaxInt64`, `MinInt64` as strings; the rejection list above; `*Centavos` with `null`, a missing key and a string.
- The wrap at the edge, without a database: `montoDeFila(repositorio.Posting{ID: 42, Monto: 9007199254740993})` is `Centavos(9007199254740993)`; `centavosOpcional(pgtype.Int8{Valid: false})` is `nil` and `centavosOpcional(pgtype.Int8{Int64: -5, Valid: true})` points at `-5`; `Saldo` through `repositorio.New` with a fake `DBTX` whose `QueryRow` returns a row that scans an `int64` (`-1000000` and `9007199254740993`) comes back as that `Centavos`; the insert path hands the driver an `int64` argument (what `InsertPostingParams.Monto` carries) and returns a `Centavos`. `pgx.Row` is a one-method interface (`Scan(dest ...any) error`), so the fake is a dozen lines and needs no container.
- The two JSON shapes side by side, so the failure mode stays visible in the suite: the raw `repositorio.Posting` marshals `"monto":9007199254740993` and the same row wrapped into a `nucleo` value marshals `"monto":"9007199254740993"`.
- The structural net: a reflect walk over the exported method set of `*Servicio` fails if any return type (through pointers and slices) has a package path ending in `/internal/repositorio`. Checked to fail on an injected leak before being trusted; it does not see an `int64` money field on a `nucleo` type, review does.

```go
func TestServicioNoExponeTiposDelRepositorio(t *testing.T) {
	tipo := reflect.TypeOf(&Servicio{})
	for i := 0; i < tipo.NumMethod(); i++ {
		m := tipo.Method(i)
		for j := 0; j < m.Type.NumOut(); j++ {
			out := m.Type.Out(j)
			for out.Kind() == reflect.Pointer || out.Kind() == reflect.Slice {
				out = out.Elem()
			}
			if strings.HasSuffix(out.PkgPath(), "/internal/repositorio") {
				t.Errorf("Servicio.%s devuelve %s: una fila cruda saldría del paquete sin envolver", m.Name, out)
			}
		}
	}
}
```

- Against a real Postgres through the `withTx` fixture: insert a posting with `InsertPostingParams{Monto: monto.Int64()}` for a `monto` past 2^53, read it back with `GetPosting` and assert `Centavos(fila.Monto)` equals the original; read `SaldoCuenta` and wrap it as `Centavos(saldo)` (unqualified: the test is in `package nucleo`).

## Anti-Patterns

- **`float64` for money anywhere.** `Balance float64` and `Amount float64` in the current skeleton, `pgtype.Numeric.Float64()`, `strconv.ParseFloat`, `%.2f`, `math.Round`. The ledger document bans it at every point of the system, the API included.
- **`NUMERIC(14,2)` money columns, and a stored `balance` column.** Both in the skeleton's migrations. Chapter 6 chose `bigint`; chapter 1 forbids the balance column outright.
- **`a + b`, `a - b` or `-c` on two `Centavos`.** They compile and wrap. `Sumar`, `Restar`, `Centavos(0).Restar(c)`.
- **`c.Sumar(-o)` as subtraction.** For `o == math.MinInt64` the negation wraps before `Sumar` can look at it; `Restar` reports that case.
- **`type Centavos = int64`.** An alias has no methods and no identity: an id fits where money belongs.
- **Any sqlc override whose `go_type` is `internal/nucleo.Centavos`**, per column (`column: "posting.monto"`) or per type (`db_type: "pg_catalog.int8"`). sqlc writes `import ".../internal/nucleo"` into `models.go`, `nucleo` already imports `repositorio` for `*repositorio.Queries`, and the build stops with `import cycle not allowed` (verified). The per-type form was wrong before this pass as well (every IDENTITY id and foreign key became `Centavos`, the nullable money columns did not); now both forms are wrong for the same reason. Money columns keep sqlc's default type, always, and moving `Centavos` out of `nucleo` to get the override back is a change to chapter 5 of the boundaries document, not to `sqlc.yaml`.
- **A `Queries` result that carries a money column and is not wrapped on the next line.** `fila.Monto` is an `int64` that compiles wherever an id does and marshals as a JSON number. `Centavos(fila.Monto)` (through the row-taking helper) before anything else touches it; `centavosOpcional` for a `pgtype.Int8`.
- **`Centavos(fila.ID)`, or a wrap helper that takes an `int64` instead of the row.** It compiles. The helper takes the row and picks the column by name, so the call site has nothing to get wrong.
- **A `repositorio` type in an exported signature of `Servicio`, or a generated row handed to `WriteData` or `json.Marshal`.** The raw `int64` leaves as a number. Only `nucleo` types cross the boundary; the reflect test pins the signatures, review pins the fields.
- **`SUM(monto)` without `::bigint`.** `interface{}` in the signature, `pgtype.Numeric` at runtime, and `.Float64()` one line away.
- **A JSON number for an amount**, outbound or inbound, "for now". The contract is a string on both sides.
- **`json:",string"` as the mechanism.** It quotes a plain `int64` too, but only on the fields that carry the tag (verified: `{"a":"1","b":2}` with the tag on `a` alone). Forget it once and the amount ships as a number; the method on the type has nothing to forget.
- **`UnmarshalJSON` that also accepts a number "in development".** It passes every small-fixture test and fails in production on the first large value.
- **A `DesdePesos(float64)` / `DesdeUnidadesMayores` constructor, or parsing `PresentarUnidadesMayores` output back.** The conversion exists once, in one direction, for one reader.
- **`monto / 100.0` in SQL, or `/ 100` in a service.** Presentation only, and only through `PresentarUnidadesMayores`.
- **A `Multiplicar`, `Porcentaje` or `Repartir` added on the way to a feature.** Each carries a rounding rule; the rule is written down and reviewed first.
- **A `Scanner`/`Valuer` on `Centavos` "to be safe".** `Centavos` never reaches pgx: the params side passes `Int64()`, the row side wraps after the scan. The extra code would only have to be kept correct.

## Related Skills

- `golang-patterns` for the named-type idiom `Centavos` follows, the sentinel errors `ErrDesbordamiento` and `ErrMontoInvalido`, `%w` and `errors.Is`.
- `sqlc-patterns` for the rest of `sqlc.yaml`, the generate-and-diff workflow, the `Querier` the money columns flow through as raw `int64`, and the transaction a posting pair is written in.
- `chi-patterns` for the envelope the amount string travels inside, the `decodeAndValidate` path where `ErrMontoInvalido` becomes a 400, and the DTO separation that is now where a generated row's `int64` becomes the string on the wire.
- `golang-bootstrap` for the composition root that constructs the `Querier`; nothing money-related lives there, and `LoadConfig` never parses an amount.
- `golang-testing` for the map-keyed table tests above and the `withTx` fixture that runs the insert-through-`Int64()`, read-back-and-wrap round trip against a real Postgres.
