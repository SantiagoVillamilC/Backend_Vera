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

# Los tres archivos se escriben siempre: no dependen de systemd ni de Docker.

# ${DOMINIO_SERVIDOR} (infra/servidor/dominio-fijado.sh) sí se interpola acá,
# por eso el heredoc va SIN comillas en el delimitador.
contenido_centinela_conf="$(cat <<EOF
# infra/servidor/nginx/conf.d/centinela.conf (generado por 30-nginx.sh, no editar a mano)
#
# location / vive en snippets/raiz-http.conf: SCRUM-30 solo reemplaza ese archivo.
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
  # contenedor de compose que ya esté corriendo.
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
