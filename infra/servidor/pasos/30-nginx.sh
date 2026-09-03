#!/usr/bin/env bash
# infra/servidor/pasos/30-nginx.sh
#
# Instala y configura nginx como proxy inverso (tarjeta SCRUM-32), corriendo
# como contenedor Docker en vez de como paquete de apt. Decisión de equipo,
# no de este script: consistente con Postgres (SCRUM-33, todavía esqueleto)
# y con la app (que ya trae Dockerfile propio, EXPOSE 8080). El razonamiento
# completo, con los pros y contras de la alternativa descartada (paquete de
# apt), vive en el comentario de Jira de SCRUM-32 y en la bitácora técnica
# del proyecto de grado, no se repite acá.
#
# nginx comparte con Postgres un único infra/servidor/compose.yaml (ver ese
# archivo): cada paso es dueño de su propio bloque bajo "services:" y corre
# "docker compose up -d <servicio>" solo sobre el suyo.
#
# Este archivo se carga con source desde aprovisionar.sh, no se ejecuta como
# proceso aparte (misma nota que 10-ssh.sh y 20-docker.sh sobre por qué no
# usa "return" para salir temprano).

NOMBRE_PASO="Instalar y configurar nginx como proxy inverso"
TARJETA="SCRUM-32"

paso "${NOMBRE_PASO} (${TARJETA})"

ARCHIVO_COMPOSE="${DIRECTORIO_SCRIPT}/compose.yaml"
DIRECTORIO_NGINX="${DIRECTORIO_SCRIPT}/nginx"

# Verificado el 2 de septiembre de 2026 con:
#   docker pull nginx:1.27.5-alpine
#   docker inspect --format '{{index .RepoDigests 0}}' nginx:1.27.5-alpine
# Digest resuelto: nginx@sha256:65645c7bb6a0661892a8b03b89d0743208a18dd2f3f17a54ef4b76fb8e2f2a10
# (ese mismo día, el tag flotante de minor "1.27-alpine" resolvía al mismo
# digest; se fija acá la versión de parche explícita, no la minor, para no
# depender de que ese tag se mueva solo el día que nginx publique 1.27.6).
# Declarada una sola vez acá, no repetida en compose.yaml: se interpola en
# el heredoc de compose.yaml de más abajo y en la verificación con
# "docker run ... nginx -t", para que el tag no pueda quedar desincronizado
# entre los dos usos.
IMAGEN_NGINX="nginx:1.27.5-alpine"

# Se escriben siempre, sin gate de systemd: son solo archivos de texto, no
# necesitan ni Docker ni systemd para crearse. La única fuente de verdad de
# su contenido son los heredocs de acá abajo, no un archivo estático aparte
# versionado en el repositorio.

# ${DOMINIO_SERVIDOR} (infra/servidor/dominio-fijado.sh) sí se interpola acá,
# por eso el heredoc va SIN comillas en el delimitador.
contenido_centinela_conf="$(cat <<EOF
# infra/servidor/nginx/conf.d/centinela.conf
#
# Generado por infra/servidor/pasos/30-nginx.sh (SCRUM-32) a partir de
# DOMINIO_SERVIDOR (infra/servidor/dominio-fijado.sh). No editar a mano: el
# próximo aprovisionamiento lo sobreescribe.
#
# El bloque "location /" vive en un archivo aparte
# (nginx/snippets/raiz-http.conf, incluido más abajo) a propósito: cuando
# SCRUM-30 (certbot) agregue el bloque 443, va a sobreescribir SOLO ese
# snippet con la redirección a HTTPS, sin tocar este archivo.
server {
    listen 80 default_server;
    server_name ${DOMINIO_SERVIDOR};

    location /.well-known/acme-challenge/ {
        root /var/www/certbot;
    }

    include /etc/nginx/snippets/raiz-http.conf;
}
EOF
)"
asegurar_archivo_con_contenido "${DIRECTORIO_NGINX}/conf.d/centinela.conf" "${contenido_centinela_conf}"

# raiz-http.conf NO tiene ninguna variable de bash que interpolar, pero SÍ
# tiene variables de nginx ($host, $remote_addr, $scheme, $api,
# $proxy_add_x_forwarded_for). Por eso este heredoc va CON comillas en el
# delimitador ("<<'EOF'"): sin las comillas, bash intentaría expandir esas
# variables de nginx como si fueran suyas, y como no están definidas en
# este script, las reemplazaría por cadenas vacías (por ejemplo
# "proxy_set_header Host ;" en vez de "proxy_set_header Host $host;"). Ese
# error es sintácticamente válido para "nginx -t" (no lo detecta), pero
# rompe el proxy en producción.
contenido_raiz_http="$(cat <<'EOF'
# infra/servidor/nginx/snippets/raiz-http.conf
#
# Generado por infra/servidor/pasos/30-nginx.sh (SCRUM-32). SCRUM-30 va a
# sobreescribir este archivo con una redirección a HTTPS cuando exista el
# certificado; hasta entonces, este es el único punto de entrada real al
# backend.
#
# "resolver 127.0.0.11" + "set $api" difieren la resolución del nombre
# "api" a tiempo de petición en vez de a tiempo de carga de la
# configuración: sin esto, "nginx -t" y el arranque del contenedor fallan
# con "host not found in upstream" mientras no exista ningún contenedor
# "api" en la red "centinela" (todavía no hay ningún paso que lo
# despliegue, ver infra/servidor/README.md).
location / {
    resolver 127.0.0.11 valid=10s;
    set $api http://api:8080;
    proxy_pass $api;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
}
EOF
)"
asegurar_archivo_con_contenido "${DIRECTORIO_NGINX}/snippets/raiz-http.conf" "${contenido_raiz_http}"

# compose.yaml tampoco tiene ninguna variable de nginx que proteger, así
# que puede ir sin comillas en el delimitador: solo interpola
# ${IMAGEN_NGINX}, declarada arriba.
contenido_compose="$(cat <<EOF
# infra/servidor/compose.yaml
#
# Único archivo de compose para todo lo que corre como contenedor Docker en
# el servidor: nginx (SCRUM-32) y, a futuro, Postgres (SCRUM-33). Cada paso
# de infra/servidor/pasos/ es dueño de su propio bloque bajo "services:" y
# corre "docker compose up -d <servicio>" sobre el suyo, sin tocar el
# bloque de otro paso.
#
# Generado por infra/servidor/pasos/30-nginx.sh con
# asegurar_archivo_con_contenido, no versionado como archivo estático
# aparte: lo que viaja al servidor por scp como parte de infra/servidor/
# (ver infra/servidor/README.md) es el SCRIPT que arma este archivo, no
# este archivo ya armado. El próximo aprovisionamiento lo sobreescribe si
# el contenido esperado cambió.
#
# "name:" fijo en vez de dejar que compose lo derive del nombre del
# directorio: infra/servidor/ se copia a rutas distintas según el destino
# (/opt/centinela/infra-servidor en Azure, /tmp/infra-servidor en Vagrant,
# /root/infra-servidor en el contenedor de prueba de probar-en-docker.sh),
# y sin este campo el nombre del proyecto (y por lo tanto el prefijo de
# los contenedores) cambiaría según el destino.
name: centinela

networks:
  centinela:
    name: centinela

services:
  nginx:
    image: ${IMAGEN_NGINX}
    container_name: centinela-nginx
    restart: unless-stopped
    ports:
      - "80:80"
    volumes:
      - ./nginx/conf.d:/etc/nginx/conf.d:ro
      - ./nginx/snippets:/etc/nginx/snippets:ro
      - ./nginx/webroot:/var/www/certbot:ro
    networks:
      - centinela
EOF
)"
asegurar_archivo_con_contenido "${ARCHIVO_COMPOSE}" "${contenido_compose}"

# Vacío por ahora: lo usa certbot a futuro (SCRUM-30) para dejar ahí el
# archivo de verificación del reto ACME que nginx sirve desde
# /.well-known/acme-challenge/ (ver centinela.conf arriba).
asegurar_directorio "${DIRECTORIO_NGINX}/webroot"

if [[ ! -d /run/systemd/system ]]; then
  # Mismo criterio que 10-ssh.sh y asegurar_servicio_activo (lib/comun.sh):
  # sin systemd como proceso 1 no hay daemon de Docker que alcanzar, así
  # que ni la prueba de configuración contra un contenedor real ni
  # "docker compose up -d nginx" pueden correr acá. Es el caso del
  # contenedor de infra/servidor/probar-en-docker.sh, que además no monta
  # el socket de Docker (ver la nota de cabecera de asegurar_servicio_compose
  # en lib/comun.sh). Los archivos de configuración sí quedaron escritos
  # arriba; lo que se omite es todo lo que depende del daemon: la prueba de
  # "nginx -t" contra un contenedor real, el contenedor nginx en sí, y la
  # verificación del puerto 80. Hay que verificarlo en la VM real (Azure o
  # Vagrant).
  aviso "systemd no es el proceso 1 en este sistema (no existe /run/systemd/system): se omite por completo la prueba de configuración y el contenedor nginx. Verifícalo en la VM real (Azure o Vagrant)."
else
  # Prueba de configuración SIN necesitar el contenedor de compose corriendo
  # todavía: un "docker run --rm" suelto, con los mismos volúmenes de solo
  # lectura que declara compose.yaml para conf.d/ y snippets/, así se
  # detecta un error de sintaxis (o el gotcha del resolver, si algún día se
  # rompe) antes de tocar el contenedor que ya esté corriendo.
  salida_prueba_nginx=""
  if ! salida_prueba_nginx="$(docker run --rm \
    -v "${DIRECTORIO_NGINX}/conf.d:/etc/nginx/conf.d:ro" \
    -v "${DIRECTORIO_NGINX}/snippets:/etc/nginx/snippets:ro" \
    "${IMAGEN_NGINX}" nginx -t 2>&1)"; then
    error_fatal "La configuración generada no pasa 'nginx -t' (imagen ${IMAGEN_NGINX}). No se tocó ningún contenedor que ya estuviera corriendo. Salida de nginx -t:
${salida_prueba_nginx}"
  fi
  info "nginx -t confirma que la configuración es válida (imagen ${IMAGEN_NGINX})."

  asegurar_servicio_compose "${ARCHIVO_COMPOSE}" nginx

  # Confirmación final de que el contenedor de verdad quedó escuchando en
  # el puerto 80: mismo patrón que 10-ssh.sh verificando sshd con "ss -tln"
  # después de reiniciarlo, en vez de confiar solo en que "docker compose
  # up" haya salido con código 0.
  if command -v ss &>/dev/null; then
    if ! ss -tln 2>/dev/null | grep -q ':80 '; then
      error_fatal "El contenedor nginx está corriendo según docker compose, pero ss -tln no lo muestra escuchando en el puerto 80. Revisa el estado del contenedor antes de continuar."
    fi
    info "Confirmado con ss -tln: nginx está escuchando en el puerto 80."
  else
    aviso "ss no está disponible: no se pudo confirmar con un socket real que nginx esté escuchando en el puerto 80. Verifícalo a mano."
  fi
fi
