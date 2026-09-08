#!/usr/bin/env bash
# infra/servidor/pasos/30-nginx.sh
# Instala y configura nginx como proxy inverso, en contenedor Docker
# (SCRUM-32). Se carga con source: nunca "exit" para salir temprano.

NOMBRE_PASO="Instalar y configurar nginx como proxy inverso"
TARJETA="SCRUM-32"

paso "${NOMBRE_PASO} (${TARJETA})"

ARCHIVO_COMPOSE="${DIRECTORIO_SCRIPT}/compose.yaml"
DIRECTORIO_NGINX="${DIRECTORIO_SCRIPT}/nginx"

# Versión de parche fijada a propósito: no cambiar a un tag flotante de minor.
IMAGEN_NGINX="nginx:1.27.5-alpine"

# UN solo predicado (existe el certificado emitido por 50-certbot.sh) decide
# las tres cosas que SCRUM-30 le agrega a este paso: (a) qué snippet incluye
# centinela.conf más abajo, (b) si el chequeo de nginx -t de este mismo paso
# monta /etc/letsencrypt, y (c) si hay que recargar el contenedor con un
# reload condicional (ver más abajo). Ver README.md, sección "30-nginx.sh".
DIRECTORIO_LETSENCRYPT="/opt/centinela/letsencrypt"
ARCHIVO_FULLCHAIN_TLS="${DIRECTORIO_LETSENCRYPT}/live/${DOMINIO_SERVIDOR}/fullchain.pem"
if [[ -f "${ARCHIVO_FULLCHAIN_TLS}" ]]; then
  SNIPPET_RAIZ_INCLUIDO="raiz-redirigir.conf"
else
  SNIPPET_RAIZ_INCLUIDO="raiz-http.conf"
fi

# Los tres archivos se escriben siempre: no dependen de systemd ni de Docker.

# Bracket para el reload condicional de más abajo: mide si centinela.conf o
# raiz-http.conf cambiaron de CONTENIDO en esta corrida (por ejemplo, porque
# el predicado de arriba cambió de snippet), sin importar si el contenedor
# se termina recreando o no (eso se mide aparte, justo antes de
# asegurar_servicio_compose).
cambios_antes_conf_nginx="$(resumen_cambios)"

# ${DOMINIO_SERVIDOR} (infra/servidor/dominio-fijado.sh) sí se interpola acá,
# por eso el heredoc va SIN comillas en el delimitador.
contenido_centinela_conf="$(cat <<EOF
# infra/servidor/nginx/conf.d/centinela.conf (generado por 30-nginx.sh, no editar a mano)
#
# location / vive en snippets/${SNIPPET_RAIZ_INCLUIDO} (predicado de arriba):
# raiz-http.conf (30-nginx.sh, proxy directo) mientras no exista el
# certificado, raiz-redirigir.conf (50-certbot.sh, redirección a HTTPS) una
# vez que sí existe. 30-nginx.sh solo decide CUÁL incluir; nunca escribe el
# contenido de raiz-redirigir.conf.
server {
    listen 80 default_server;
    server_name ${DOMINIO_SERVIDOR};

    location /.well-known/acme-challenge/ {
        root /var/www/certbot;
    }

    include /etc/nginx/snippets/${SNIPPET_RAIZ_INCLUIDO};
}
EOF
)"
asegurar_archivo_con_contenido "${DIRECTORIO_NGINX}/conf.d/centinela.conf" "${contenido_centinela_conf}"

# CON comillas en el delimitador: el contenido usa variables de nginx
# ($host, $api...), no de bash; sin comillas, bash las expandiría a vacío.
contenido_raiz_http="$(cat <<'EOF'
# infra/servidor/nginx/snippets/raiz-http.conf (generado por 30-nginx.sh, no editar a mano)
#
# resolver + set $api difieren la resolución de "api" a tiempo de petición:
# ver infra/servidor/README.md.
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

cambios_despues_conf_nginx="$(resumen_cambios)"

# Sin variables de nginx que proteger: puede ir sin comillas en el delimitador.
contenido_compose="$(cat <<EOF
# infra/servidor/compose.yaml (generado por 30-nginx.sh, no versionado, no editar a mano)
#
# Dueño único de "name:" y "networks:" top-level. Postgres agrega su propio
# compose.postgres.yaml con solo su bloque "services:": ver infra/servidor/README.md.
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

# Vacío por ahora: SCRUM-30 (certbot) lo va a usar para el reto ACME.
asegurar_directorio "${DIRECTORIO_NGINX}/webroot"

if [[ ! -d /run/systemd/system ]]; then
  aviso "systemd no es el proceso 1 en este sistema (no existe /run/systemd/system): se omite por completo la prueba de configuración y el contenedor nginx. Verifícalo en la VM real (Azure o Vagrant)."
else
  # nginx -t contra un "docker run --rm" suelto, antes de tocar el
  # contenedor de compose que ya esté corriendo. Monta /etc/letsencrypt SOLO
  # si el certificado ya existe (mismo predicado de arriba): en cuanto
  # 50-certbot.sh haya escrito nginx/conf.d/centinela-tls.conf (referencia
  # rutas bajo /etc/letsencrypt), nginx -t falla con "cannot load
  # certificate" sin este mount, incluso contra este chequeo suelto.
  comando_prueba_nginx=(docker run --rm
    -v "${DIRECTORIO_NGINX}/conf.d:/etc/nginx/conf.d:ro"
    -v "${DIRECTORIO_NGINX}/snippets:/etc/nginx/snippets:ro")
  if [[ -f "${ARCHIVO_FULLCHAIN_TLS}" ]]; then
    comando_prueba_nginx+=(-v "${DIRECTORIO_LETSENCRYPT}:/etc/letsencrypt:ro")
  fi
  comando_prueba_nginx+=("${IMAGEN_NGINX}" nginx -t)

  salida_prueba_nginx=""
  if ! salida_prueba_nginx="$("${comando_prueba_nginx[@]}" 2>&1)"; then
    error_fatal "La configuración generada no pasa 'nginx -t' (imagen ${IMAGEN_NGINX}). No se tocó ningún contenedor que ya estuviera corriendo. Salida de nginx -t:
${salida_prueba_nginx}"
  fi
  info "nginx -t confirma que la configuración es válida (imagen ${IMAGEN_NGINX})."

  cambios_antes_compose_nginx="$(resumen_cambios)"
  asegurar_servicio_compose "${ARCHIVO_COMPOSE}" nginx

  # Reload condicional: si el CONTENIDO de centinela.conf cambió (el
  # predicado de arriba pasó de un snippet a otro) pero asegurar_servicio_compose
  # no recreó el contenedor (por ejemplo, porque compose.certbot.yaml ya
  # existía de una corrida anterior y no aportó ningún cambio nuevo a nivel
  # de compose), el contenedor sigue corriendo con el archivo VIEJO montado
  # en memoria hasta que se le avise. Mismo patrón que el reinicio
  # condicional de sshd en 10-ssh.sh: comparar resumen_cambios antes/después.
  # Nunca se ejecuta si asegurar_servicio_compose ya recreó el contenedor
  # (ese recreate ya monta el archivo nuevo desde cero, un reload ahí sería
  # redundante, no incorrecto).
  if [[ "${cambios_antes_conf_nginx}" -lt "${cambios_despues_conf_nginx}" && "${cambios_antes_compose_nginx}" -eq "$(resumen_cambios)" ]]; then
    info "La configuración de nginx cambió de contenido, pero el contenedor no se recreó: se recarga con un reload (docker kill -s HUP)."
    if ! docker kill -s HUP centinela-nginx; then
      error_fatal "No se pudo recargar el contenedor centinela-nginx (docker kill -s HUP) después de que su configuración cambió de contenido."
    fi
    info "Confirmado: se envió SIGHUP a centinela-nginx para recargar la configuración nueva."
  fi

  # ss -tln confirma que nginx de verdad quedó escuchando, no solo que
  # docker compose devolvió 0.
  if command -v ss &>/dev/null; then
    if ! ss -tln 2>/dev/null | grep -q ':80 '; then
      error_fatal "El contenedor nginx está corriendo según docker compose, pero ss -tln no lo muestra escuchando en el puerto 80. Revisa el estado del contenedor antes de continuar."
    fi
    info "Confirmado con ss -tln: nginx está escuchando en el puerto 80."
  else
    aviso "ss no está disponible: no se pudo confirmar con un socket real que nginx esté escuchando en el puerto 80. Verifícalo a mano."
  fi
fi
