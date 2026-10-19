#!/usr/bin/env bash
# infra/servidor/pasos/65-simuladores.sh
# Instala los tres servicios externos simulados como contenedores aparte.

NOMBRE_PASO="Instalar los servicios simulados (riel, custodio y PSE)"
TARJETA="SCRUM-114"

paso "${NOMBRE_PASO} (${TARJETA})"

DIRECTORIO_SIMULADORES="/opt/centinela/simuladores"
ARCHIVO_COMPOSE="${DIRECTORIO_SIMULADORES}/compose.yaml"
ARCHIVO_ENV="${DIRECTORIO_SIMULADORES}/.env"
ARCHIVO_PLANTILLA="${DIRECTORIO_SCRIPT}/simuladores/simuladores.yaml"
ARCHIVO_TOKEN_API="/opt/centinela/api/custodio-token.env"

# cd-simuladores.yml publica un tag por rama; deploy-ci.sh reescribe TAG en
# .env en cada despliegue, así que aquí solo se fija el punto de partida.
REGISTRO_SIMULADORES="ghcr.io/santiagovillamilc/backend_vera"
TAG_SIMULADORES="${TAG_SIMULADORES:-main}"
PUERTO_PSE="8091"

asegurar_directorio "${DIRECTORIO_SIMULADORES}" "700"
asegurar_archivo_con_contenido "${ARCHIVO_COMPOSE}" "$(cat "${ARCHIVO_PLANTILLA}")"

# El token es el mismo que usa la API para autenticar al custodio. No se genera otro.
# shellcheck source=/dev/null
. "${ARCHIVO_TOKEN_API}"
if [[ -z "${CUSTODIO_TOKEN:-}" || "${#CUSTODIO_TOKEN}" -lt 32 ]]; then
  error_fatal "CUSTODIO_TOKEN no existe o tiene menos de 32 bytes en ${ARCHIVO_TOKEN_API}."
fi

contenido_env="$(cat <<EOF
REGISTRO=${REGISTRO_SIMULADORES}
TAG=${TAG_SIMULADORES}
PSE_HOST_PORT=${PUERTO_PSE}
CUSTODIO_TOKEN=${CUSTODIO_TOKEN}
NUCLEO_CONFIRMACION_URL=http://api:8080/v1/depositos/confirmaciones
SIMULADOR_CUSTODIA_URL=http://simulador-custodia:8099/notificaciones/depositos
EOF
)"
asegurar_archivo_con_contenido "${ARCHIVO_ENV}" "${contenido_env}"
chmod 600 "${ARCHIVO_ENV}"

if [[ ! -d /run/systemd/system ]]; then
  aviso "systemd no es el proceso 1 en este sistema (no existe /run/systemd/system): se omite por completo el arranque de los simuladores. Verifícalo en la VM real (Azure o Vagrant)."
else
  (
    cd "${DIRECTORIO_SIMULADORES}" || exit 1
    docker compose pull
  )

  for servicio in simulador-riel simulador-custodia simulador-pse; do
    asegurar_servicio_compose "${ARCHIVO_COMPOSE}" "${servicio}"
  done

  for contenedor in vera-simulador-riel vera-simulador-custodia vera-simulador-pse; do
    intentos=0
    sano="no"
    while [[ "${intentos}" -lt 30 ]]; do
      if [[ "$(docker inspect -f '{{.State.Health.Status}}' "${contenedor}" 2>/dev/null)" == "healthy" ]]; then
        sano="si"
        break
      fi
      intentos=$((intentos + 1))
      sleep 2
    done
    if [[ "${sano}" != "si" ]]; then
      error_fatal "El contenedor ${contenedor} no quedó healthy tras 60 segundos. Revisa 'docker logs ${contenedor}'."
    fi
    info "Confirmado con el healthcheck: ${contenedor} está listo."
  done
fi

# Borde HTTP de PSE. Es el único de los tres con nombre público: el navegador
# del usuario tiene que abrir la página de pago. Las locaciones viven en un
# snippet que solo incluye el bloque de :443; el de :80 sirve el desafío ACME y
# redirige, para que la página donde se autoriza el pago nunca viaje en claro.
DIRECTORIO_NGINX="${DIRECTORIO_SCRIPT}/nginx"
ARCHIVO_SNIPPET_PSE="${DIRECTORIO_NGINX}/snippets/pse-http.conf"
ARCHIVO_PSE_HTTP="${DIRECTORIO_NGINX}/conf.d/pse.conf"
ARCHIVO_PSE_TLS="${DIRECTORIO_NGINX}/conf.d/pse-tls.conf"
ARCHIVO_PLANTILLA_PSE="${DIRECTORIO_SCRIPT}/simuladores/pse-http.conf"
DIRECTORIO_LETSENCRYPT="/opt/centinela/letsencrypt"
ARCHIVO_FULLCHAIN_PSE="${DIRECTORIO_LETSENCRYPT}/live/${DOMINIO_PSE}/fullchain.pem"

# Misma imagen que 50-certbot.sh: un solo certbot para las dos emisiones.
IMAGEN_CERTBOT="${IMAGEN_CERTBOT:-certbot/certbot:v5.8.0}"
CERTBOT_STAGING="${CERTBOT_STAGING:-no}"

asegurar_directorio "${DIRECTORIO_NGINX}/snippets"
asegurar_directorio "${DIRECTORIO_NGINX}/conf.d"
asegurar_directorio "${DIRECTORIO_NGINX}/webroot"

cambios_antes_borde="$(resumen_cambios)"

recargar_nginx() {
  if docker inspect centinela-nginx >/dev/null 2>&1; then
    # 30-nginx.sh no vuelve a correr entre este paso y el anterior, así que el
    # vhost nuevo entra con una recarga explícita del contenedor que ya corre.
    docker kill -s HUP centinela-nginx >/dev/null
  fi
}

asegurar_archivo_con_contenido "${ARCHIVO_SNIPPET_PSE}" "$(cat "${ARCHIVO_PLANTILLA_PSE}")"

# El :80 nunca incluye el snippet del proxy: sirve el desafío ACME (si no,
# "certbot renew" falla, porque el webroot vive detrás de este bloque) y, con
# el certificado ya emitido, redirige todo a HTTPS. Mientras no haya
# certificado, sí proxya, para que el flujo funcione en HTTP antes de la
# primera emisión.
escribir_pse_http() {
  local contenido

  if [[ -f "${ARCHIVO_FULLCHAIN_PSE}" ]]; then
    contenido="$(cat <<EOF
# infra/servidor/nginx/conf.d/pse.conf (generado por 65-simuladores.sh, no editar a mano)
server {
    listen 80;
    server_name ${DOMINIO_PSE};

    location ^~ /.well-known/acme-challenge/ {
        root /var/www/certbot;
    }

    location / {
        return 301 https://\$host\$request_uri;
    }
}
EOF
)"
  else
    contenido="$(cat <<EOF
# infra/servidor/nginx/conf.d/pse.conf (generado por 65-simuladores.sh, no editar a mano)
server {
    listen 80;
    server_name ${DOMINIO_PSE};

    location ^~ /.well-known/acme-challenge/ {
        root /var/www/certbot;
    }

    include /etc/nginx/snippets/pse-http.conf;
}
EOF
)"
  fi

  asegurar_archivo_con_contenido "${ARCHIVO_PSE_HTTP}" "${contenido}"
}

escribir_pse_http
# La recarga va antes de certbot: el desafío tiene que estar servido cuando
# Let's Encrypt lo pida.
recargar_nginx

if [[ -f "${ARCHIVO_FULLCHAIN_PSE}" ]]; then
  info "El certificado de ${DOMINIO_PSE} ya existe en ${ARCHIVO_FULLCHAIN_PSE}: no se reemite."
elif [[ -z "${CORREO_CERTBOT:-}" ]]; then
  aviso "Falta CORREO_CERTBOT (aprovisionar.env): ${DOMINIO_PSE} se queda solo en HTTP hasta definirlo."
elif [[ ! -d /run/systemd/system ]]; then
  aviso "systemd no es el proceso 1 en este sistema: no se emite el certificado de ${DOMINIO_PSE}."
else
  comando_certonly=(certonly --webroot -w /var/www/certbot
    -d "${DOMINIO_PSE}" -m "${CORREO_CERTBOT}" --agree-tos --non-interactive)
  if [[ "${CERTBOT_STAGING}" == "si" ]]; then
    comando_certonly+=(--staging)
  fi
  info "Emitiendo el certificado para ${DOMINIO_PSE} (correo ${CORREO_CERTBOT}, staging=${CERTBOT_STAGING})."
  if ! docker run --rm \
    -v "${DIRECTORIO_NGINX}/webroot:/var/www/certbot" \
    -v "${DIRECTORIO_LETSENCRYPT}:/etc/letsencrypt" \
    "${IMAGEN_CERTBOT}" "${comando_certonly[@]}"; then
    error_fatal "certbot certonly falló al emitir el certificado para ${DOMINIO_PSE}. Confirma que el registro A existe y apunta a esta máquina; Let's Encrypt limita a 5 validaciones fallidas por hora (usa CERTBOT_STAGING=si para probar sin gastar ese límite)."
  fi
  if [[ ! -f "${ARCHIVO_FULLCHAIN_PSE}" ]]; then
    error_fatal "certbot certonly terminó sin error, pero ${ARCHIVO_FULLCHAIN_PSE} no existe."
  fi
  info "Certificado emitido: ${ARCHIVO_FULLCHAIN_PSE} existe."
  registrar_cambio
fi

if [[ -f "${ARCHIVO_FULLCHAIN_PSE}" ]]; then
  contenido_pse_tls="$(cat <<EOF
# infra/servidor/nginx/conf.d/pse-tls.conf (generado por 65-simuladores.sh, no editar a mano)
server {
    listen 443 ssl;
    http2 on;
    server_name ${DOMINIO_PSE};

    ssl_certificate /etc/letsencrypt/live/${DOMINIO_PSE}/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/${DOMINIO_PSE}/privkey.pem;

    add_header Strict-Transport-Security "max-age=31536000" always;

    include /etc/nginx/snippets/pse-http.conf;
}
EOF
)"
  asegurar_archivo_con_contenido "${ARCHIVO_PSE_TLS}" "${contenido_pse_tls}"
  # Con el certificado emitido, el :80 deja de proxear y pasa a redirigir.
  escribir_pse_http
fi

if [[ "$(resumen_cambios)" != "${cambios_antes_borde}" ]]; then
  recargar_nginx
  info "nginx recargado para tomar el vhost de ${DOMINIO_PSE}."
fi
