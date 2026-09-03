#!/usr/bin/env bash
# infra/servidor/pasos/40-postgres.sh
#
# Instala y configura Postgres en contenedor Docker (tarjeta SCRUM-33). El
# volumen de datos es un bind mount a una ruta absoluta del host, FUERA de
# cualquier árbol que un "docker compose down -v" pueda gestionar como
# volumen nombrado. La tarjeta lo dice de forma literal: "El volumen va
# declarado fuera del contenedor. Un docker compose down -v mal dado borra
# la base." Un volumen nombrado de Docker es justo lo que borra "down -v";
# un bind mount a una ruta del host que compose nunca creó, no.
#
# Este archivo se carga con source desde aprovisionar.sh, no se ejecuta como
# proceso aparte (misma nota que 10-ssh.sh, 20-docker.sh y 30-nginx.sh sobre
# por qué no usa "return" para salir temprano).
#
# Comparte con nginx (30-nginx.sh) la misma red "centinela" y el mismo
# mecanismo de fusión de archivos de compose en tiempo de "up -d"
# (asegurar_servicio_compose, lib/comun.sh), pero NO comparte archivo: este
# paso escribe su propio compose.postgres.yaml, con solamente su bloque
# "services: postgres:", sin repetir "name:" ni el bloque top-level
# "networks:" que ya declaró compose.yaml. Ver el comentario de cabecera del
# heredoc más abajo para el porqué completo.

NOMBRE_PASO="Instalar y configurar Postgres en contenedor"
TARJETA="SCRUM-33"

paso "${NOMBRE_PASO} (${TARJETA})"

# Verificado el 3 de septiembre de 2026 con:
#   docker pull postgres:16-alpine
#   docker inspect --format '{{index .RepoDigests 0}}' postgres:16-alpine
# Digest resuelto:
#   postgres@sha256:cf78e76683b9ca8c5733cbbdce6c9262b45b6767934dd0a95e671f9a0fc20685
# Ese mismo día, el tag de parche exacto "16.15-alpine3.24" (Postgres 16.15
# sobre Alpine 3.24) resolvía al mismo digest, confirmado por separado con
# "docker pull postgres:16.15-alpine3.24" seguido de
# "docker inspect --format '{{index .RepoDigests 0}}' postgres:16.15-alpine3.24".
# Se fija acá ese tag de parche exacto, no el minor flotante "16-alpine",
# por el mismo motivo que IMAGEN_NGINX en 30-nginx.sh: no depender de que el
# tag flotante se mueva solo el día que Postgres publique 16.16-alpine.
# Variante "-alpine" por el mismo criterio que nginx: imagen más chica.
IMAGEN_POSTGRES="postgres:16.15-alpine3.24"

ARCHIVO_COMPOSE_POSTGRES="${DIRECTORIO_SCRIPT}/compose.postgres.yaml"
DIRECTORIO_DATOS_POSTGRES="/opt/centinela/postgres/datos"
ARCHIVO_SECRETO_POSTGRES="/opt/centinela/postgres/postgres.env"

# Se crea siempre, sin gate de systemd: es solo un directorio del
# filesystem, no necesita Docker para existir. El 700 es el permiso del
# directorio, no su dueño: la imagen oficial de Postgres arranca su
# contenedor como root y hace "chown" del datadir al usuario "postgres"
# dentro de su propio entrypoint, antes de bajar privilegios para correr
# "initdb" (UID 70 en la variante -alpine que usa este paso, UID 999 en las
# variantes basadas en Debian: son imágenes distintas con su
# propio /etc/passwd). No se asigna dueño desde acá porque ese chown ocurre
# DENTRO del contenedor, no en este script; se verificó por separado, con
# el contenedor corriendo (ver infra/servidor/README.md, sección
# "40-postgres.sh"), que el resultado es justamente UID 70 y modo 700.
#
# La ruta vive en /opt/centinela/postgres/, fuera de DIRECTORIO_SCRIPT
# (infra/servidor/) a propósito: infra/servidor/ completo viaja por scp a
# rutas que cambian según el destino (/opt/centinela/infra-servidor en
# Azure, /tmp/infra-servidor en Vagrant, /root/infra-servidor en el
# contenedor de infra/servidor/probar-en-docker.sh), y /tmp además se limpia
# en cada reinicio de Debian (ver infra/servidor/README.md). Un volumen
# dentro de ese árbol se perdería en el primer reinicio de la VM, o
# apuntaría a una ruta distinta según dónde se ejecute el aprovisionamiento.
asegurar_directorio "${DIRECTORIO_DATOS_POSTGRES}" "700"

# Se asegura siempre, sin gate de systemd: es solo un archivo de texto, no
# necesita Docker para existir. Ver el comentario de cabecera de
# asegurar_archivo_con_secreto (lib/comun.sh) para el diseño completo: se
# genera una sola vez y se persiste en disco, así que nunca hay que decidir
# "es la primera corrida o no" (el propio archivo es la fuente de verdad), y
# deja ya resuelta la fuente de la que un futuro paso de despliegue de la
# app (todavía sin tarjeta en Jira) podría leer las credenciales de
# conexión.
#
# El valor que quede acá solo importa en el primer arranque del contenedor:
# initdb lo usa para crear el rol "centinela" la primera vez que ve el
# directorio de datos vacío. Si alguien edita este archivo después, no
# cambia nada en la base ya inicializada; rotar la contraseña de verdad
# requiere "ALTER ROLE centinela WITH PASSWORD '...'" dentro de la base, no
# solo tocar el archivo.
asegurar_archivo_con_secreto "${ARCHIVO_SECRETO_POSTGRES}" "POSTGRES_PASSWORD"

# compose.postgres.yaml no tiene ninguna variable de Postgres que proteger
# (a diferencia de nginx/snippets/raiz-http.conf con las variables propias
# de nginx), así que el heredoc va sin comillas en el delimitador: interpola
# ${IMAGEN_POSTGRES}, ${DIRECTORIO_DATOS_POSTGRES} y
# ${ARCHIVO_SECRETO_POSTGRES}, las tres declaradas una sola vez arriba.
#
# Diseño multi-archivo: este paso es dueño de su propio compose*.yaml, en
# vez de que reescriba el compose.yaml completo de 30-nginx.sh (eso
# duplicaría el bloque de nginx acá, con el riesgo real de que quedara
# desincronizado si alguien edita un paso sin el otro) o de que ambos
# servicios se mezclen a mano en el mismo heredoc (eso volvería a los dos
# pasos dueños del mismo archivo, perdiendo el aislamiento que ya tiene cada
# uno sobre "su" bloque de compose). Este archivo declara solo
# "services: postgres:", sin "name:" ni el bloque top-level "networks:":
# esos ya los declaró compose.yaml (ver ese archivo, generado por
# 30-nginx.sh), y docker compose fusiona las claves de nivel superior entre
# todos los "-f" que reciba en una misma invocación. asegurar_servicio_compose
# (lib/comun.sh) es quien arma esa lista de "-f" en tiempo de "up -d", a
# partir de todos los compose*.yaml que encuentre en el directorio, así que
# el orden en que corran 30-nginx.sh y 40-postgres.sh no importa: cualquiera
# de los dos que dispare "up -d" primero fusiona los fragmentos que ya
# existan en ese momento. El servicio "postgres" solo necesita
# "networks: - centinela" para unirse a la red que compose.yaml ya declaró.
#
# Sin "ports:": a diferencia de nginx (que sí publica el 80 porque es el
# punto de entrada público del servidor), Postgres no necesita quedar
# accesible desde fuera del host. Solo lo consume la app (contenedor "api",
# todavía sin ningún paso que lo despliegue, ver infra/servidor/README.md)
# dentro de la misma red "centinela", por nombre de contenedor. Es el mismo
# argumento de seguridad que ya usa 30-nginx.sh para no publicar el 8080 de
# "api" hacia el host: menos superficie expuesta es mejor que más, cuando
# nada externo necesita hablarle directo a este servicio.
contenido_compose_postgres="$(cat <<EOF
# infra/servidor/compose.postgres.yaml
#
# Generado por infra/servidor/pasos/40-postgres.sh (SCRUM-33) con
# asegurar_archivo_con_contenido, no versionado como archivo estático
# aparte: lo que viaja al servidor por scp como parte de infra/servidor/
# es el SCRIPT que arma este archivo, no este archivo ya armado. El próximo
# aprovisionamiento lo sobreescribe si el contenido esperado cambió. Ver el
# comentario de 40-postgres.sh, justo antes de este heredoc, para el diseño
# multi-archivo (por qué este archivo no repite "name:" ni "networks:") y
# para el porqué de la ausencia de "ports:".
services:
  postgres:
    image: ${IMAGEN_POSTGRES}
    container_name: centinela-postgres
    restart: unless-stopped
    environment:
      POSTGRES_USER: centinela
      POSTGRES_DB: centinela
    env_file:
      - ${ARCHIVO_SECRETO_POSTGRES}
    volumes:
      - ${DIRECTORIO_DATOS_POSTGRES}:/var/lib/postgresql/data
    networks:
      - centinela
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U centinela -d centinela"]
      interval: 10s
      timeout: 5s
      retries: 5
EOF
)"
asegurar_archivo_con_contenido "${ARCHIVO_COMPOSE_POSTGRES}" "${contenido_compose_postgres}"

if [[ ! -d /run/systemd/system ]]; then
  # Mismo criterio que 20-docker.sh y 30-nginx.sh: sin systemd como proceso 1
  # no hay daemon de Docker que alcanzar, así que ni "docker compose up -d
  # postgres" ni la verificación con pg_isready pueden correr acá. Es el caso
  # del contenedor de infra/servidor/probar-en-docker.sh. Los archivos de
  # datos y de configuración sí quedaron asegurados arriba (el directorio de
  # datos, el secreto y compose.postgres.yaml); lo que se omite es todo lo
  # que depende del daemon: el contenedor Postgres en sí, pg_isready, y la
  # verificación de que el puerto 5432 no quede publicado. Hay que
  # verificarlo en la VM real (Azure o Vagrant).
  aviso "systemd no es el proceso 1 en este sistema (no existe /run/systemd/system): se omite por completo el contenedor Postgres. Verifícalo en la VM real (Azure o Vagrant)."
else
  asegurar_servicio_compose "${ARCHIVO_COMPOSE_POSTGRES}" postgres

  # "docker compose up -d" devuelve el control en cuanto el contenedor
  # arranca, no cuando Postgres termina de inicializar: la primera vez que
  # ve el directorio de datos vacío, initdb tarda varios segundos. Un único
  # pg_isready inmediatamente después de asegurar_servicio_compose fallaría
  # casi siempre en la primera corrida real (el contenedor "corriendo" según
  # docker compose todavía no acepta conexiones), así que se reintenta en
  # una ventana acotada en vez de cortar con error_fatal al primer intento.
  # "-h localhost" fuerza la conexión por TCP: durante la inicialización, el
  # entrypoint de la imagen levanta un servidor temporal escuchando solo por
  # socket Unix, y un pg_isready sin "-h" puede reportar "listo" contra ese
  # servidor temporal en vez de contra el servidor real. El healthcheck del
  # propio compose.postgres.yaml, en cambio, se deja tal cual sin "-h": ese
  # test lo evalúa Docker desde dentro del propio contenedor a intervalos
  # regulares, no una sola vez bajo presión de tiempo como esta verificación.
  intentos_listo=0
  postgres_listo="no"
  while [[ "${intentos_listo}" -lt 30 ]]; do
    if docker exec centinela-postgres pg_isready -U centinela -d centinela -h localhost &>/dev/null; then
      postgres_listo="si"
      break
    fi
    intentos_listo=$((intentos_listo + 1))
    sleep 2
  done

  if [[ "${postgres_listo}" != "si" ]]; then
    error_fatal "El contenedor centinela-postgres está corriendo según docker compose, pero pg_isready no confirmó que Postgres esté listo tras 30 intentos (60 segundos). Revisa el estado del contenedor antes de continuar."
  fi
  info "Confirmado con pg_isready: Postgres está listo dentro del contenedor."

  # Verificación negativa, no positiva como la de nginx con el puerto 80:
  # Postgres NO debe aparecer escuchando en ningún puerto del host. Si
  # "ss -tln" encontrara el 5432, sería señal de que algo publicó el puerto
  # sin que nadie lo pidiera (un "ports:" agregado por error, una fusión de
  # compose que no salió como se esperaba), justo lo que este diseño quiere
  # evitar. Mismo criterio de graceful degradar que 30-nginx.sh si "ss" no
  # está disponible.
  if command -v ss &>/dev/null; then
    if ss -tln 2>/dev/null | grep -q ':5432 '; then
      error_fatal "ss -tln muestra el puerto 5432 escuchando en el host: Postgres no debería estar publicado. Revisa compose.postgres.yaml."
    fi
    info "Confirmado con ss -tln: el puerto 5432 no está expuesto en el host."
  else
    aviso "ss no está disponible: no se pudo confirmar con un socket real que el puerto 5432 no esté expuesto en el host. Verifícalo a mano."
  fi
fi
