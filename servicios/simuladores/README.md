# Simuladores Vera

Monorepo de los tres sistemas externos que el proyecto simula. Cada uno es un proceso
independiente, con su propio almacenamiento y su propio contrato. No comparten base ni código
con el backend principal.

| Servicio | Sistema real que representa | Puerto | Almacenamiento |
|---|---|---|---|
| `cmd/simulador-riel` | Riel de pagos inmediatos Bre-B y directorio de llaves | 8098 | SQLite propio |
| `cmd/simulador-custodia` | Banco custodio de la cuenta ómnibus | 8099 | SQLite propio |
| `cmd/simulador-pse` | Pasarela PSE y página de autorización del banco | 8090 | SQLite propio |

Los contratos exactos viven en `contratos/`. Los consume el backend Go de
`Backend_Vera` por HTTP, con las variables `RIEL_SIMULADOR_URL`,
`DIRECTORIO_SIMULADOR_URL`, `CUSTODIO_EXTRACTO_URL`, `PSE_URL` y `CUSTODIO_TOKEN`.

## Origen

El código vivía bajo `cmd/` del módulo raíz y compilaba con el binario del API. Ahora es un
módulo Go anidado dentro del mismo repositorio (`servicios/simuladores`), con binarios y
despliegue independientes. El módulo raíz lo ignora al compilar y al correr sus pruebas.

La pasarela PSE canónica es la de este módulo (`cmd/simulador-pse`). El repositorio
`PSE_Vera` (Vue 3 + Node) fue un prototipo que solo notificaba al núcleo; no dispara la pata
del banco custodio y por eso no puede sostener el invariante 2.

## Uso local

```sh
make test
make build
```

Cada servicio se levanta con su entorno:

```sh
SIMULADOR_RIEL_DB=/tmp/riel.db \
SIMULADOR_RIEL_PORT=8098 \
SIMULADOR_RIEL_PERMITIR_EXPOSICION=true \
SIMULADOR_RIEL_ADDR=0.0.0.0 \
go run ./cmd/simulador-riel
```

## Despliegue

```sh
cd deploy
cp .env.example .env
# ajusta CUSTODIO_TOKEN con un valor de 32 bytes o más
docker compose up -d --build
```

- Los tres servicios se conectan a la red Docker `centinela`, que comparten con el API y
  nginx.
- Los volúmenes conservan los nombres de los compose anteriores
  (`simulador-riel_datos-simulador-riel` y `simulador-custodia_datos-simulador-custodia`), así
  que no se pierde el saldo ni el extracto al migrar.
- El PSE publica en `127.0.0.1:8091` mientras el PSE anterior ocupe el 8090. El vhost apunta
  al nombre del contenedor, no al puerto del host.
- El vhost de `pse.veraapp.tech` está en `deploy/nginx/pse.veraapp.tech.conf` y exige
  `proxy_set_header Host $host`, por la validación de `Origin`.

## Propiedad

- `cmd/simulador-riel`: Santiago.
- `cmd/simulador-custodia` y `cmd/simulador-pse`: Christian.

Cualquier cambio de contrato se hace primero en `contratos/` y después en el código. El
backend no cambia cuando cambia la implementación del simulador.
