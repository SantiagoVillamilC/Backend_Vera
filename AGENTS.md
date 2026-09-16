# AGENTS.md: Backend_Centinela

Backend en Go de la billetera digital que es el proyecto de fin de grado de UCompensar. Repositorio
de trabajo en equipo: no es tarea de una sola persona, así que este archivo existe para que cualquier
integrante que use un LLM para programar arranque con las mismas líneas base, en vez de que cada
quien invente su propio estilo y termine pisando el trabajo de otro.

## Por qué hay un solo archivo

`AGENTS.md` es el archivo que lee OpenCode como contexto de proyecto, siempre, de forma estática,
sin selección (ver https://opencode.ai/docs/rules/). `CLAUDE.md` es el que espera Claude Code (ver
https://code.claude.com/docs/en/skills). Son dos herramientas distintas que dos personas del equipo
pueden estar usando en paralelo, y el contenido que necesitan es el mismo. En vez de mantener dos
archivos sincronizados a mano, `CLAUDE.md` es un symlink a este archivo:

```bash
ln -s AGENTS.md CLAUDE.md
```

Si editas contexto de proyecto, edita `AGENTS.md`. `CLAUDE.md` nunca se toca directamente, es solo
el puntero. El objetivo de fondo es que esto funcione igual para quien use Claude Code y para quien
use OpenRouter a través de OpenCode: cualquiera de los dos lee el mismo contenido, sin que nadie
tenga que mantener dos copias sincronizadas a mano.

Nota para quien esté en Windows: con la configuración por defecto de git (`core.symlinks=false`),
un `git clone` deja `CLAUDE.md` como un archivo de texto plano que solo contiene la palabra
`AGENTS.md`, no como un symlink funcional. Si te pasa, corre `git config core.symlinks true` antes
de clonar (o usa WSL), o simplemente lee `AGENTS.md` directamente, es el mismo contenido.

## Dónde viven las skills y cómo las encuentra cada herramienta

Las skills están en `.claude/skills/<nombre>/SKILL.md`. Tanto Claude Code como OpenCode las
descubren ahí sin configuración adicional: OpenCode reconoce `.claude/skills/` como una de sus rutas
nativas por compatibilidad con Claude Code. Por eso no existe una carpeta `.opencode/skills/`
separada: sería la misma información duplicada en dos formatos, y ya con el símlink de arriba queda
claro que la idea de este repo es evitar copias que se puedan desincronizar.

## Cómo se disparan

El mecanismo principal es el matching semántico nativo de cada herramienta: el agente lee la
`description` del frontmatter de cada `SKILL.md` y decide si el pedido del usuario encaja, sin que
nadie tenga que invocar la skill por nombre.

Como refuerzo determinístico (para no depender solo de que el modelo "se acuerde" de buscar), hay un
hook en `.claude/hooks/go-skill-router.sh`, conectado en `.claude/settings.json`, que se dispara en
cada mensaje o antes de cada uso de herramienta: detecta trabajo relacionado con cada skill y
sugiere cargarla. Es una capa adicional, no un reemplazo del matching semántico.

Esto solo aplica a Claude Code: los hooks de `.claude/settings.json` son un mecanismo propio de esa
herramienta. Si usas OpenCode, las skills se descubren igual (por `.claude/skills/`), pero este
refuerzo determinístico no corre ahí, dependes solo del matching semántico nativo de OpenCode.

## Las 10 skills instaladas

Todas viven en `.claude/skills/`. La mayoría declara en su propio `SKILL.md` con qué otra skill
limita y a cuál redirigir cuando el tema no le pertenece, así que si una respuesta parece
incompleta, es probable que la continuación esté en la skill vecina que ella misma nombra.

- **`golang-patterns`**: el idioma en sí. Structs y receivers en vez de clases, interfaces
  implícitas, patrones GoF re-expresados como composición, manejo de errores (`%w`,
  `errors.Is`/`As`), concurrencia (goroutines, channels, `context`, worker pools), generics, y el
  toolchain (`go.mod`, `golangci-lint` v2, `gofmt`/`goimports`).
- **`golang-testing`**: cómo se testea en este stack. Tests table-driven, fakes escritos a mano en
  vez de librerías de mocking, tests de integración contra Postgres real vía testcontainers-go,
  `-race` y `-cover` con un umbral real en CI, benchmarks con `b.Loop`.
- **`version-standards`**: las versiones canónicas exactas de Go, chi, sqlc, pgx, goose,
  golangci-lint y el resto del stack de test, más la regla de dependencias exactas (nunca `^` ni
  `~`) y la estrategia de runners self-hosted. Se consulta al tocar `go.mod`, `.golangci.yml`,
  workflows o Dockerfiles.
- **`chi-patterns`**: cómo se arma la API HTTP con `go-chi/chi` v5 sobre `net/http` estándar.
  Setup del router y agrupación de rutas, la cadena de middleware y su orden, binding y validación
  con `go-playground/validator`, DTOs tipados y el envelope obligatorio `{success, data, error,
  meta}`.
- **`sqlc-patterns`**: la capa de acceso a datos. sqlc (codegen SQL-first) sobre pgx v5 contra
  Postgres autoalojado, con Goose para migraciones. Cubre `sqlc.yaml`, sizing del pool, queries
  `:one`/`:many`/`:exec`, transacciones con `pgx.Tx`, mapeo de errores SQLSTATE, paginación por
  cursor y el ciclo completo de migraciones Goose.
- **`golang-bootstrap`**: cómo arranca y se apaga un servicio Go. El composition root en
  `cmd/api/main.go`, timeouts de `http.Server`, el apagado ordenado por señales (HTTP primero, jobs
  después, pool al final), validación de configuración al boot que reporta todos los errores juntos,
  `/healthz` vs `/readyz`, y jobs de fondo con river.
- **`golang-websocket`**: push en tiempo real con `github.com/coder/websocket`. La regla de que un
  socket transmite señales, nunca estado (el cliente siempre puede releer por REST), cómo montar la
  ruta de upgrade sin heredar `middleware.Timeout`, autenticación del handshake, keepalive con
  `Ping`, y el registro de conexiones que el apagado ordenado tiene que drenar.
- **`golang-docs`**: documentación de un servicio Go. La convención de comentarios godoc (sin tags
  tipo `@param`/`@return`), los linters de staticcheck que la hacen cumplir, y el contrato OpenAPI
  escrito a mano con `oapi-codegen` v2 generando tipos y la interfaz de servidor de chi.
- **`golang-money`**: cómo se representa, se suma y se serializa un monto de dinero en este proyecto
  específico. Documenta el diseño que ya fijaron los dos documentos de arquitectura de la tesis
  (ver más abajo): enteros de unidades menores en `bigint`, nunca `float`/`double`/`NUMERIC`, y
  montos que viajan como string en JSON porque un `int64` completo no cabe en el rango seguro de un
  número de JavaScript.
- **`golang-logging`**: logging estructurado con `log/slog` de la librería estándar, sin logger de
  terceros. El handler JSON en producción y texto en desarrollo, cómo se estampa el `request_id` de
  chi en cada línea, y cómo se evita que `Config` imprima secretos como `JWT_SECRET`.

## Una nota honesta: esto no cubre todo, todavía

Este set de 10 no nació de una lista cerrada, nació de ir cubriendo huecos a medida que aparecían.
Ya hay temas identificados y conscientemente pendientes: contenerización
Go específica (el `Dockerfile` actual ya es correcto a mano, pero ninguna skill lo respalda si
alguien lo toca), `govulncheck`/`gosec` integrados en CI, y build/release o fuzzing (deprioritizados
a propósito para un despliegue a un único VPS). Si en el trabajo diario aparece un hueco distinto,
la respuesta correcta es proponer una skill nueva o ampliar una existente, no trabajar alrededor del
hueco y dejarlo sin decir nada.

## Estado real del código hoy vs. lo que las skills documentan

Esto es importante para no asumir de más: varias de estas skills documentan hacia dónde va el
diseño del backend, no necesariamente lo que el código ya implementa hoy. Esta sección se actualiza
a medida que el código avanza.

- `go.mod` declara `go 1.27` con `toolchain go1.27.0`, y el stack que las skills asumen ya está
  wireado: chi v5, pgx v5.10.0, goose v3, river v0.44.1, validator v10 y uuid. `go.sum` está
  poblado.
- `internal/wallets` e `internal/transactions` ya no existen: sus paquetes se borraron y la
  migración `0006_drop_wallets_transactions.sql` dropea sus tablas. El dinero vive en
  `internal/nucleo` con el tipo `nucleo.Centavos` (entero de centavos, `int64`), lo que
  `golang-money` exige.
- El router es chi (`internal/platform/httpserver/router.go`), con el envelope
  `{success, data, error, meta}` y los grupos público y autenticado de SCRUM-51. La autenticación
  sigue siendo un usuario de prueba inyectado detrás de una interfaz, no JWT real.
- El esquema se migra con goose desde `bd/migraciones`; el ledger vive en el esquema Postgres
  `nucleo` y River en el suyo. Hay dos paquetes sqlc: `internal/repositorio` (auth) y
  `internal/nucleo/internal/repositorio` (núcleo). Toda consulta del núcleo corre dentro del helper
  de transacción que hace `SET LOCAL ROLE rol_nucleo`.
- El diseño que `golang-money` documenta viene de dos borradores de arquitectura de la tesis,
  citados por ruta relativa dentro de la skill: `../nucleo-financiero-ledger.md` (cómo se
  representa el dinero, capítulo 6) y `../frontera-modulos-nucleo-financiero.md` (en qué paquete Go
  vive, capítulo 5).

  Nota importante: esos dos documentos no están versionados en ningún repositorio todavía, ni en
  `Backend_Centinela` ni en el repo personal de Christian. Es a propósito: son borradores de lógica
  de negocio que todavía no se han validado del todo, y no se quiere meter esa lógica al setup
  compartido del equipo antes de que esté validada. Consecuencia práctica: esas dos rutas solo
  resuelven en la máquina de Christian ahora mismo, así que si el código y `golang-money` parecen
  contradecirse en cómo se maneja el dinero, trata el contenido de la skill como el diseño
  propuesto (no confirmado como definitivo) hasta que esos documentos se versionen en algún lado.

En resumen: las skills son la línea base para código nuevo, no una descripción de lo que ya existe.
No asumas que un módulo ya sigue el patrón de una skill solo porque la skill existe, verifícalo en el
archivo real primero.
