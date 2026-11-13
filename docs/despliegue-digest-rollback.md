# Despliegue por digest, modelo fijado y vuelta atrás (R-05)

`infra/servidor/deploy-ci.sh` es el comando forzado de la llave de CI. Antes desplegaba por etiqueta
de rama (`:main`), no guardaba el entorno anterior y no tenía vuelta atrás. Ahora acepta un digest
inmutable y un tag de modelo, los valida antes de tocar nada, respalda el entorno y tiene rollback.

## Uso

```bash
# Despliegue normal por rama (lo que manda cd.yml)
deploy-ci.sh main

# Por digest inmutable y con el modelo del lockfile
deploy-ci.sh main --digest sha256:<64 hex> --modelo gemma-4-e2b-it-q4_k_m

# Vuelta atrás al último respaldo
deploy-ci.sh --rollback

# Prueba en seco sin docker
DEPLOY_SIMULAR=si DEPLOY_DIRECTORIO=/tmp/x deploy-ci.sh main --digest sha256:<64 hex>
```

Las variables de entorno equivalentes son `IMAGEN_DIGEST` y `MODELO_TAG`. La rama sigue siendo el
primer argumento y sigue validándose contra `dev`, `staging` y `main`; `--rollback` no la necesita.

## Qué valida y en qué orden

1. Rama permitida (`dev`, `staging`, `main`), código 64 si no.
2. Formato del digest: `sha256:` y 64 hexadecimales, código 65 si no. Sin simular, además
   `docker manifest inspect <registro>@<digest>`: un digest inexistente aborta con código 66 **antes**
   de tocar el entorno vigente.
3. Tag de modelo contra `modelos/lock.json`: solo se aceptan los nombres del lockfile, código 68 si
   no. Sin lockfile, código 67.
4. Solo entonces respalda y reescribe `IMAGEN_API` (`registro@digest` o `registro:rama`) y
   `MODELO_TAG` en los dos `.env`.

## Respaldo y rollback

Cada despliegue guarda una copia fechada (`AAAA-MM-DDTHH-MM-SSZ`) con `api.env` y
`simuladores.env` en `respaldos/`. `--rollback` restaura la copia más reciente, levanta la API con la
imagen anterior, espera `healthy` y corre el humo. Sin respaldos aborta con código 69.

Límites declarados:

- Las migraciones **no** se revierten: volver a una imagen anterior con la base ya migrada es una
  decisión consciente y el rollback solo restaura imagen y modelo. Si la migración incompatible ya
  corrió, la vuelta atrás exige restaurar la base por separado.
- El GGUF del modelo vive en el volume de pesos: volver a un tag anterior no lo redescarga si el
  archivo sigue ahí; si se borró, el primer arranque lo baja de nuevo.
- El humo es `GET /readyz` (configurable con `DEPLOY_SMOKE_URL`). No sustituye una prueba funcional.

## Códigos de salida

| Código | Significado |
| --- | --- |
| 0 | Despliegue o rollback terminó, healthy y con humo OK |
| 64 | Rama o argumento no permitido |
| 65 | Digest con formato inválido |
| 66 | Digest inexistente en el registro |
| 67 | Falta el lockfile de modelos |
| 68 | Tag de modelo que no está en el lockfile |
| 69 | Rollback sin respaldos |

## Prueba en seco

```bash
bash -n infra/servidor/deploy-ci.sh
bash infra/servidor/deploy-ci_test.sh
```

`deploy-ci_test.sh` usa un directorio temporal, un lockfile mínimo y `DEPLOY_SIMULAR=si`; cubre
digest y modelo válidos con respaldo, digest inválido sin tocar el entorno, modelo inválido, rollback
con respaldo que restaura imagen y modelo, rollback sin respaldo y rama inválida. No se corrió
shellcheck (no está instalado en esta VPS); la sintaxis se comprobó con `bash -n`.
