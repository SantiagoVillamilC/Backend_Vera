#!/usr/bin/env bash
# go-skill-router: reminds the model to load the matching Go skill from
# .claude/skills/ when the prompt, an edited file, or a file path touches the
# surface one of the ten Go skills owns. Idempotent per session (each skill is
# reminded at most once per session_id). Structural twin of
# ~/.claude/hooks/db-skill-router.sh, which does the same for the DB skills.
# Wired from .claude/settings.json to UserPromptSubmit and PreToolUse(Edit|Write|Read).
# Reads the hook JSON on stdin; prints hookSpecificOutput.additionalContext, or nothing.
#
# Skills routed (all in .claude/skills/<name>/SKILL.md):
#   golang-patterns   golang-testing   version-standards  chi-patterns  sqlc-patterns
#   golang-bootstrap  golang-websocket golang-docs        golang-money  golang-logging
#
# Two rules keep the matching honest:
#   1. Word-boundary regexes only (grep -E with \b), never bare substrings.
#   2. Words that mean something else in this repo are never used bare:
#      "transaction" is a money transfer here (internal/transactions), not pgx.Tx;
#      "wallet" is the whole product; "push", "log", "commit" are git; "workflow"
#      is the Workflow tool; "pin" is the MFA code; "handler" is HTTP, slog or river;
#      "float64" also times the access log. Each of those is matched qualified.

input="$(cat)"

event="$(printf '%s' "$input" | jq -r '.hook_event_name // ""' 2>/dev/null)"
session="$(printf '%s' "$input" | jq -r '.session_id // "nosession"' 2>/dev/null)"
[ -z "$session" ] && session="nosession"

fp="$(printf '%s' "$input" | jq -r '.tool_input.file_path // ""' 2>/dev/null)"

# Three scanning tiers, decided by the target path:
#   meta: skill, memory and .claude files. Their PATH already carries skill names
#         (.claude/skills/sqlc-patterns/SKILL.md), and their content mentions every
#         technology in prose, so only the prompt and the command are scanned.
#   doc:  markdown, text, diagrams, LaTeX, and non-Go source (the React deck in
#         diapositiva-planos/ names "sqlc", "chi" and "saldo" as data, and TS has
#         its own "interface", "routes" and "test"). Prose mentions "sqlc" without
#         doing sqlc work, so the path and the prompt are scanned but not the content.
#   code: everything else (Go, SQL, YAML, Dockerfile...). Content is scanned too.
tier=code
case "$fp" in
  */.claude/*|*/skills/*|*/memory/*) tier=meta ;;
  *.md|*.mdx|*.markdown|*.txt|*.puml|*.d2|*.tex|*.bib) tier=doc ;;
  *.ts|*.tsx|*.js|*.jsx|*.mjs|*.cjs|*.css|*.scss|*.html|*.dart|*.json) tier=doc ;;
esac

case "$tier" in
  meta)
    text="$(printf '%s' "$input" | jq -r '
      [ .prompt,
        (.tool_input.command // empty)
      ] | map(select(. != null)) | join(" ")' 2>/dev/null)" ;;
  doc)
    text="$(printf '%s' "$input" | jq -r '
      [ .prompt,
        (.tool_input.file_path // empty),
        (.tool_input.command   // empty)
      ] | map(select(. != null)) | join(" ")' 2>/dev/null)" ;;
  *)
    text="$(printf '%s' "$input" | jq -r '
      [ .prompt,
        (.tool_input.file_path   // empty),
        (.tool_input.content     // empty),
        (.tool_input.new_string  // empty),
        (.tool_input.old_string  // empty),
        (.tool_input.command     // empty)
      ] | map(select(. != null)) | join(" ")' 2>/dev/null)" ;;
esac
# No fallback to the raw JSON: it would re-inject the file path the meta tier
# deliberately left out. Nothing to scan means nothing to say.
[ -z "$text" ] && [ -z "$fp" ] && exit 0
lc="$(printf '%s' "$text" | tr '[:upper:]' '[:lower:]')"

# first <ERE>: prints the first token the regex matches in the scanned text.
first() { printf '%s' "$lc" | grep -Eo "$1" 2>/dev/null | head -n1; }

# mark <skill> <reason>: records why a skill fired. The first reason wins.
mark() {
  local v="why_${1//-/_}"
  [ -n "${!v}" ] || printf -v "$v" '%s' "$2"
}

# match <skill> <ERE>: marks the skill with the first token the regex matched.
# A token that is a path (the .go filename rule matches the whole path) is
# reduced to its basename so the reminder reads "main.go", not a 40-char prefix.
match() {
  local m
  m="$(first "$2")"
  [ -z "$m" ] && return 1
  case "$m" in */*) m="${m##*/}" ;; esac
  mark "$1" "${m:0:40}"
}

# ---------------------------------------------------------------------------
# Text rules (prompt, path, content). One regex per skill, alternations grouped
# by the "When to Activate" section of that skill. Accented Spanish is written
# as (o|ó) rather than [oó]: a bracket is byte-wise under a C locale and would
# silently stop matching "transacción" on a teammate's shell.
# ---------------------------------------------------------------------------

# golang-patterns: the language itself. Any .go file, go.mod, golangci-lint,
# errors, concurrency, context, generics, structs and interfaces.
match golang-patterns '\bgolang\b|\bgoroutines?\b|\bgo\.mod\b|\bgo\.sum\b|\bgolangci(-lint)?\b|\berrgroup\b|\bgofmt\b|\bgoimports\b|\bgo (build|vet|run|mod|fmt|tool|doc|fix)\b|\bcontext\.context\b|\berrors\.(is|as|join|new)\b|\bfmt\.errorf\b|\bsync\.(waitgroup|mutex|rwmutex|once|oncevalue)\b|\bselect \{|\bchan\b|<-chan\b|chan<-|[a-z0-9_/.-]+\.go\b|\bstructs?\b|\binterfaces?\b|\binterfaz\b|\bgenerics?\b|\bgen(e|é)ricos?\b|\bpanic\b|\brecover\(\)|\btype parameters?\b|\bpackage layout\b|\bgo 1\.[0-9]+\b|\bgo1\.[0-9]+|\bworker pool\b|\bfunctional options\b|\bembedding\b|\breceivers?\b'

# golang-testing: _test.go, go test, fakes, httptest, testcontainers, race,
# coverage, benchmarks. Bare "test"/"pruebas" stays in on purpose: with
# golang-patterns this is one of the two general skills of the set.
match golang-testing '_test\.go\b|\bgo test\b|\btesting\.[tbm]\b|\bt\.(run|parallel|cleanup|helper|fatalf?|errorf?|context|tempdir|setenv)\(|\bhttptest\b|\btestcontainers\b|\bdockertest\b|\btable.driven\b|\bcmp\.diff\b|\bgo-cmp\b|\btestify\b|\bgomock\b|\bmockgen\b|go\.uber\.org/mock|\bmoq\b|\bgoleak\b|\bb\.loop\(\)|\bbenchmarks?\b|\btestmain\b|\bsynctest\b|(^|[[:space:]])-race\b|(^|[[:space:]])-cover(profile|pkg|mode)?\b|\bunit tests?\b|\bpruebas? unitarias?\b|\bintegration tests?\b|\bpruebas? de integraci(o|ó)n\b|\btdd\b|\bfakes?\b|\bmocks?\b|\btests?\b|\btesting\b|\bpruebas?\b|\bcobertura\b|\bcoverage\b'

# version-standards: pinned versions, go.mod directives, .golangci.yml,
# GitHub Actions and runners, Dockerfile base images, dependency bumps.
# "version", "upgrade", "pin" and "dependency" are matched qualified: bare they
# mean a websocket upgrade, the MFA PIN, or dependency injection.
match version-standards '\bgo\.mod\b|\.golangci\.ya?ml|\bdockerfile\b|\bfrom golang:|\bgolang:[0-9]|\bgithub actions\b|\.github/workflows|\bruns-on\b|\bactions/(checkout|setup-go|setup-node|cache)\b|\buses: |\bdependabot\b|\bself-hosted\b|\bvps-tunodo\b|\btoolchain\b|\bgo 1\.[0-9]+\b|\bgo1\.[0-9]+|@v[0-9]+\.[0-9]+|\bpinned\b|\bpinning\b|\bpinear\b|\bpin (the |la |las |los |el )?(version|versi(o|ó)n|actions?|acci(o|ó)n|dependenc)|\bsha.pin|\bversi(o|ó)n(es)? (de|del|can(o|ó)nica|est(a|á)ndar|exacta|exactas|actual|fijada)\b|\b(qu(e|é)|which|what|latest|canonical|exact|current) versions?\b|\bversions? (of|table|pin)\b|\bbump\b|\bupgrade (go|chi|sqlc|pgx|goose|golangci|the (go )?toolchain|dependenc|to (go|chi|sqlc|pgx|goose|v[0-9]))\b|\b(add|update|upgrade|bump|pin|install|agrega|agregar|actualiza|actualizar|instala|instalar|sube|subir) (a |the |una |la |las |los )?dependenc(y|ies|ia|ias)\b|\bdependenc(y|ies|ia|ias) (version|versi(o|ó)n|exacta|exact|pinned)|\bpackage\.json\b|\bpnpm\b|\bnode-version\b|\bgo-version\b|\bgo get\b|\bgo install\b|\bchi/v5\b|\bpgx/v5\b|\bgoose/v3\b|\bsqlc-dev\b|\bci\b|\bci/cd\b|\bcaret\b|\btilde\b'

# chi-patterns: routes, resource Routes() trees, middleware and its order,
# CORS, rate limiting, envelope, request binding and validation, DTOs, and
# porting off net/http ServeMux (what internal/platform/httpserver uses today).
# Bare "net/http" and "handler" are not used: llm_client.go and ocr_client.go
# are outbound HTTP clients, and "handler" is also slog.Handler.
match chi-patterns '\bchi\b|\bgo-chi\b|\bmiddlewares?\b|\brouters?\b|\brouting\b|\benrutad?or\b|\benrutamiento\b|\bendpoints?\b|\broutes?\b|\bnueva ruta\b|\bruta (http|rest|get|post|put|patch|delete)\b|\brutas (http|rest|del api|de la api)\b|\b(get|post|put|patch|delete) /[a-z{]|\bhttp\.(handler|handlerfunc|responsewriter|request|maxbytesreader|servemux|newservemux|notfound|error|status[a-z]+)\b|\*http\.request\b|\bhandlefunc\(|\bservemux\b|\bgorilla/mux\b|\bcors\b|\brate.?limit[a-z]*\b|\bhttprate\b|\brequestid\b|\bclientipfrom|\bgetclientip\b|\brealip\b|\brecoverer\b|\bmiddleware\.timeout\b|\brequireauth\b|\benvelope\b|\benvoltorio\b|\bsuccess, ?data, ?error, ?meta\b|\bwritedata\b|\bwriteerror\b|\bwriteapierror\b|\bdecodeandvalidate\b|\bgo-playground\b|\bvalidator\b|validate:"|\bdtos?\b|\bapi rest\b|\brest api\b|\bapi http\b|\bhttp api\b|\bhandlers? (http|rest)\b|\b(http|rest) handlers?\b|\burlparam\b|\bpath params?\b|\bquery params?\b|\brequest body\b|\bcuerpo de la (petici(o|ó)n|solicitud|request)\b|\b(bind|binding|decode|decodificar|validar|validate|validaci(o|ó)n) (el |the |del |la |of the )?(body|json|request|petici(o|ó)n|cuerpo)\b|\bstatus codes?\b|\bc(o|ó)digos? de estado\b'

# sqlc-patterns: sqlc queries and codegen, pgx pool, DB transactions (qualified:
# pgx.Tx, tx.Begin, WithTx, never the bare noun), Goose migrations, SQL DDL,
# SQLSTATE mapping, DSN and TLS, "Go database work in general".
# Spanish "migración" is qualified because AGENTS.md calls the chi/sqlc move
# "la migración que se viene".
match sqlc-patterns '\bsqlc\b|\bquerier\b|\bdbtx\b|\bdbstore\b|\brepositor(y|ies|io|ios)\b|\bpgx\b|\bpgxpool\b|\bpgconn\b|\bpgtype\b|\bpgerrcode\b|\bjackc/|\bgoose\b|\+goose\b|\bgoose_db_version\b|\bmigrations?\b|\bmigraci(o|ó)n(es)? (sql|goose|de (la |el |las |los )?(base|bd|db|tabla|tablas|esquema|schema|columna))\b|\bmigrar (la |el |una |las |los )?(base|bd|db|tabla|tablas|esquema|schema|columna)\b|\.sql\b|\bsql\b|-- name:|(^|[[:space:]]):(one|many|exec|execrows|execresult|copyfrom)\b|\bsqlc\.(arg|narg|embed|slice)\b|\bselect .* from\b|\binsert into\b|\bupdate [a-z_]+ set\b|\bdelete from\b|\bcreate (table|index|unique index|extension)\b|\balter table\b|\bdrop (table|index|column)\b|\bpgx\.tx\b|\btx\.(begin|commit|rollback)\b|\bpool\.begin\b|\bwithtx\b|\bbegin\(ctx\)|\btransacci(o|ó)n(es)? (de |a )?(base de datos|bd|db|sql|postgres)\b|\bdb transactions?\b|\bdatabase transactions?\b|\bconnection pool\b|\bpool de conexiones\b|\bmaxconns\b|\bminidleconns\b|\bpostgres(ql)?\b|\bbase de datos\b|\bdatabases?\b|\bdb\b|\bdsn\b|\bdatabase_url\b|\bsslmode\b|\bsslrootcert\b|\bsqlstate\b|\bunique.?violation\b|\bforeign.?key\b|\bpgx\.errnorows\b|\bsql\.errnorows\b|\bcursor pagination\b|\bpaginaci(o|ó)n por cursor\b|\bkeyset\b|\bexpand.and.contract\b|\bbackfill\b|\bconcurrently\b|\bno transaction\b|\b(nueva|nuevas|new) (tabla|tablas|columna|columnas|table|tables|column|columns)\b|\b(agrega|agregar|agregue|add|adding|crea|crear|create|elimina|eliminar|drop|renombra|renombrar|rename) (una |a |la |the |las |el )?(tabla|tablas|columna|columnas|table|tables|column|columns|index|índice|indice)\b'

# golang-bootstrap: main.go and the composition root, Config/LoadConfig and env
# vars, http.Server timeouts, signals and graceful shutdown, /healthz and
# /readyz, river jobs. Bare "config", "port" and "worker" are left out.
match golang-bootstrap '\bmain\.go\b|\bconfig\.go\b|\bcmd/api\b|\bcmd/[a-z]+/main\.go|\bfunc main\(\)|\bfunc run\(\) error|\bloadconfig\b|\btype config struct\b|\bconfig struct\b|\bos\.getenv\b|\bos\.lookupenv\b|\bgetenv\b|\benvor\b|\bvariables? de entorno\b|\benvironment variables?\b|\benv vars?\b|\b\.env\b|\bhealthz\b|\breadyz\b|\bhealth ?checks?\b|\bliveness\b|\breadiness\b|\bprobes?\b|\bsondas?\b|\bgraceful\b|\bshutdown\b|\bapagado\b|\bsrv\.shutdown\b|\bsignal\.notifycontext\b|\bsigterm\b|\bsigint\b|\bos\.interrupt\b|\bos\.exit\b|\briver\b|\briverqueue\b|\briverpgxv5\b|\brivermigrate\b|\binserttx\b|\bjobargs\b|\bworkerdefaults\b|\bbackground jobs?\b|\btrabajos? en segundo plano\b|\bjob queue\b|\bcola de (trabajos|jobs)\b|\bhttp\.server\b|\breadheadertimeout\b|\bidletimeout\b|\bwritetimeout\b|\breadtimeout\b|\blog\.fatal|\bcomposition root\b|\bra(i|í)z de composici(o|ó)n\b|\blistenandserve\b|\bbootstrap\b|\barranque\b|\bboot\b|\bstartup\b|\bjwt_secret\b|\bdatabase_url\b'

# golang-websocket: coder/websocket, Accept and OriginPatterns, keepalive,
# registry, backpressure, hijacked connections in shutdown, push to the mobile
# app, real time (internal/transactions/realtime.go today). "push" only
# qualified: bare it is git push.
match golang-websocket '\bwebsockets?\b|\bws\b|\bcoder/websocket\b|\bnhooyr\b|\bgorilla/websocket\b|\bx/net/websocket\b|\bwebsocket\.(accept|dial|conn|statuscode|status[a-z]+|acceptoptions|dialoptions)\b|\bacceptoptions\b|\boriginpatterns\b|\bcheckorigin\b|\bwsjson\b|\bcloseread\b|\bkeepalive\b|\bkeep-alive\b|\bping/pong\b|\bping.?pong\b|\bpong\b|\bconnection registry\b|\bregistro de conexiones\b|\bregistry\.(add|remove|notify|closeall|wait)\b|\bws\.registry\b|\bpush (notifications?|al m(o|ó)vil|to the (mobile )?app|a la app)\b|\bnotificaci(o|ó)n(es)? push\b|\btiempo real\b|\breal.?time\b|\brealtimepublisher\b|\bregisteronshutdown\b|\bhijack|\bslow consumer\b|\bbackpressure\b|\bcontrapresi(o|ó)n\b|\bsockets?\b|\bstatusgoingaway\b|\bstatuspolicyviolation\b|\bstatusnormalclosure\b|\bcswsh\b|\bupgrade (route|handler|request|handshake|http)\b|\bruta de upgrade\b|\bhandshake\b|\bsse\b|\bserver-sent\b'

# golang-docs: doc comments and package comments, the ST1000/ST1020-22 linters,
# the OpenAPI contract and oapi-codegen, go generate, swag/huma proposals.
# "documentar"/"document" only qualified: bare it is the thesis paperwork.
match golang-docs '\bopenapi\b|\bswagger\b|\boapi-codegen\b|\boapi codegen\b|\bserverinterface\b|\bhandlerwithoptions\b|\bchiserveroptions\b|\berrorhandlerfunc\b|\bx-oapi-codegen|\bx-go-type\b|\bhuma\b|\bswaggo\b|\bswag init\b|@summary|@router|\bgodoc\b|\bgo doc\b|\bdoc comments?\b|\bcomentarios? (de )?(documentaci(o|ó)n|godoc|doc)\b|\bdoc\.go\b|\bpackage comments?\b|\bcomentario de(l)? paquete\b|\bjavadoc\b|@param\b|@return\b|@throws\b|\bst1000\b|\bst1020\b|\bst1021\b|\bst1022\b|\bpackage-comments\b|\bstaticcheck\.checks|\bexclusions\.presets|presets: \[comments\]|\bcontrato (de la |del )?api\b|\bapi contract\b|\bespecificaci(o|ó)n (de la |del )?api\b|\bapi spec\b|\bgo generate\b|//go:generate|\bopenapi\.gen\.go\b|\bdocumenta(r)? (el |la |los |las |un |una |este |esta )?(paquete|funci(o|ó)n|tipo|api|endpoint|handler|c(o|ó)digo|struct|m(e|é)todo)\b|\bdocument (the |a |this )?(package|function|type|api|endpoint|code|struct|method|handler)\b|\bcode generated\b|\bdo not edit\b|\bkin-openapi\b|\boapirequestvalidator\b|\bstrict-server\b|\bcomment linters?\b'

# golang-money: nucleo.Centavos and everything that represents an amount:
# balances, fees, limits, postings, reconciliation, bigint/NUMERIC columns,
# amounts on the wire. "float64" is left out (it also times the access log);
# NUMERIC(, pgtype.Numeric and ParseFloat are the float markers kept. "wallet"
# is left out (it is the whole product); the wallets/transactions paths are
# routed by the path rules below, which the skill itself enumerates.
match golang-money '\bcentavos\b|\bnucleo\.centavos\b|\bparsearcentavos\b|\bpresentarunidadesmayores\b|\berrdesbordamiento\b|\berrmontoinvalido\b|\.sumar\(|\.restar\(|\bmontos?\b|\bsaldos?\b|\bbalances?\b|\bamounts?\b|\bdinero\b|\bmoney\b|\bplata\b|\bpesos?\b|\bcop\b|\bcomisi(o|ó)n(es)?\b|\bfees?\b|\btarifas?\b|\bl(i|í)mites? de (monto|transferencia|transacci(o|ó)n|cupo)\b|\bcupo\b|\bpostings?\b|\bledger\b|\blibro mayor\b|\bpartida doble\b|\bdouble.entry\b|\bconciliaci(o|ó)n\b|\breconciliation\b|\bdescuadre\b|\bbigint\b|\bnumeric\(|\bpgtype\.numeric\b|\bparsefloat\b|\bunidades (menores|mayores)\b|\bminor units\b|\bmajor units\b|\bcents\b|\bcentavo\b|\btransferencias?\b|\btransferir\b|\btransfers?\b|\bmoneda\b|\bcurrency\b|\bsum\(|\bmonto_maximo\b|\bsaldo_corte\b|\bjournal_entry\b|\bnucleo-financiero-ledger\b|\bfrontera-modulos\b|\bn(u|ú)cleo\b'

# golang-logging: log/slog, handlers, slog.SetDefault, LogValuer, request_id
# correlation, access log, sloglint, third-party loggers, log.Printf and
# fmt.Println used as logging. Bare "log" is left out (git log, bitácora).
match golang-logging '\bslog\b|\blog/slog\b|\bsloglint\b|\blogvalue\b|\blogvaluer\b|\bsetdefault\b|\bnewjsonhandler\b|\bnewtexthandler\b|\bhandleroptions\b|\bdiscardhandler\b|\blog_level\b|\blog_format\b|\bloglevel\b|\blogformat\b|\blog\.(print|printf|println|fatal|fatalf|fatalln|panic|panicf|new|default)\b|\bfmt\.print(ln|f)?\(|\blogging\b|\bloggers?\b|\bstructured log|\blogs? estructurados?\b|\bl(i|í)neas? de log\b|\blog lines?\b|\blog calls?\b|\bllamadas? (de|al) log\b|\baccess ?log\b|\brequest_id\b|\brequestlogger\b|\bmiddleware\.logger\b|\bgetreqid\b|\bgetlogentry\b|\blogentry\b|\blogformatter\b|\bcorrelat[a-z]*\b|\bcorrelaci(o|ó)n\b|\bzap\b|\bzerolog\b|\blogrus\b|\bredact|\bsecrets? in (the )?logs?\b|\bsecretos? en (los )?logs?\b|\blogs? (json|estructurados?|structured)\b|\bnivel de log\b|\blog level\b|\bregistrar (un |el )?(error|evento|log)\b'

# ---------------------------------------------------------------------------
# Path rules. They cover today's tree (internal/platform/{httpserver,database,
# config}, */handler.go, */repository.go, transactions/realtime.go) and the
# layout the skills document (internal/api, internal/ws, internal/nucleo,
# db/queries, db/migrations, internal/dbstore). Only the path is looked at,
# so a Read (which carries no content) still routes.
# ---------------------------------------------------------------------------
case "$fp" in
  *_test.go) mark golang-testing 'path *_test.go'; mark golang-patterns 'path *.go' ;;
  *.go)      mark golang-patterns 'path *.go' ;;
esac
case "$fp" in
  */go.mod|*/go.sum)                 mark golang-patterns 'path go.mod'; mark version-standards 'path go.mod' ;;
  */.golangci.yml|*/.golangci.yaml)  mark golang-patterns 'path .golangci.yml'; mark version-standards 'path .golangci.yml' ;;
  */.github/workflows/*)             mark version-standards 'path .github/workflows/*' ;;
  */dependabot.yml|*/dependabot.yaml) mark version-standards 'path dependabot.yml' ;;
  */Dockerfile*|*/dockerfile*)       mark version-standards 'path Dockerfile' ;;
  */package.json)                    mark version-standards 'path package.json' ;;
esac
case "$fp" in
  */handler.go|*/httpserver/*|*/internal/api/*|*/internal/middleware/*|*/router.go|*/envelope.go)
    mark chi-patterns "path $(basename "$fp")" ;;
esac
case "$fp" in
  *.sql|*/migrations/*|*/db/queries/*|*/sqlc.yaml|*/sqlc.yml|*/sqlc.json|*/dbstore/*|*/repositorio/*|*/repository.go|*/platform/database/*|*/internal/db/*)
    mark sqlc-patterns "path $(basename "$fp")" ;;
esac
case "$fp" in
  */main.go|*/cmd/*|*/config.go|*/platform/config/*|*/health.go|*/river*.go)
    mark golang-bootstrap "path $(basename "$fp")" ;;
esac
case "$fp" in
  */internal/ws/*|*/websocket*|*/realtime*.go)
    mark golang-websocket "path $(basename "$fp")" ;;
esac
case "$fp" in
  */openapi.yaml|*/openapi.yml|*/oapi-codegen.yaml|*/oapi-codegen.yml|*/doc.go|*.gen.go|*/api/*.yaml|*/api/*.yml)
    mark golang-docs "path $(basename "$fp")" ;;
esac
case "$fp" in
  */internal/nucleo/*|*/internal/wallets/*|*/internal/transactions/*|*/nucleo.go)
    mark golang-money "path $(basename "$fp")" ;;
esac
case "$fp" in
  */internal/logging/*|*/logging.go|*/logger.go)
    mark golang-logging "path $(basename "$fp")" ;;
esac

# ---------------------------------------------------------------------------
# Emit, in canonical order, only the skills not yet reminded this session.
# ---------------------------------------------------------------------------
order="golang-patterns golang-testing version-standards chi-patterns sqlc-patterns golang-bootstrap golang-websocket golang-docs golang-money golang-logging"

cache="${HOME}/.claude/.cache/go-skill-router/${session}"
mkdir -p "$cache" 2>/dev/null

list=""
for s in $order; do
  v="why_${s//-/_}"
  reason="${!v}"
  [ -z "$reason" ] && continue
  [ -e "${cache}/${s}" ] && continue
  : > "${cache}/${s}" 2>/dev/null
  list="${list}${list:+, }${s} (trigger: ${reason})"
done
[ -z "$list" ] && exit 0

msg="Go stack work detected. Before continuing, load with the Skill tool: ${list}. These are the project's Go skills in .claude/skills/ (chi v5, sqlc/pgx, Goose, slog, coder/websocket, nucleo.Centavos); each one states its own boundaries and names the sibling skill to load when the topic moves. Automatic router: .claude/hooks/go-skill-router.sh, see AGENTS.md."

ev="${event:-UserPromptSubmit}"
jq -cn --arg ev "$ev" --arg ctx "$msg" \
  '{hookSpecificOutput: {hookEventName: $ev, additionalContext: $ctx}}'
