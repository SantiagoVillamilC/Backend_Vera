#!/usr/bin/env bash
# infra/servidor/pasos/50-certbot.sh
# Emite el certificado TLS con certbot y programa su renovación (SCRUM-30).
# Se carga con source: nunca "return" para salir temprano (ver cabecera de
# 00-verificar-sistema.sh); los guards de salto usan if/elif, igual que ahí.
#
# Reparto de archivos frente a 30-nginx.sh (ver README.md, sección
# "30-nginx.sh"): este paso NUNCA reescribe raiz-http.conf ni toca el
# contenedor nginx directamente. Solo deja archivos en el disco
# (compose.certbot.yaml, nginx/conf.d/centinela-tls.conf,
# nginx/snippets/raiz-redirigir.conf); es 30-nginx.sh, que llama a
# asegurar_servicio_compose en cada corrida, quien los recoge la PRÓXIMA vez
# que corra completo. Eso difiere en una corrida la activación real del
# puerto 443 y de la redirección a HTTPS: ver README.md para los números
# exactos.

NOMBRE_PASO="Emitir el certificado TLS con certbot y su temporizador"
TARJETA="SCRUM-30"

paso "${NOMBRE_PASO} (${TARJETA})"

# Versión estable actual, no "latest" (tag flotante) ni "nightly": confirmada
# el 5 de septiembre de 2026 contra la API de tags de Docker Hub y con
# "docker pull" + "docker inspect" directo (ver README.md).
IMAGEN_CERTBOT="certbot/certbot:v5.8.0"

DIRECTORIO_NGINX="${DIRECTORIO_SCRIPT}/nginx"
DIRECTORIO_WEBROOT="${DIRECTORIO_NGINX}/webroot"
ARCHIVO_COMPOSE_CERTBOT="${DIRECTORIO_SCRIPT}/compose.certbot.yaml"

# Mismos nombres y mismos valores que el predicado de 30-nginx.sh: es el
# mismo certificado visto desde el otro lado del reparto de archivos.
DIRECTORIO_LETSENCRYPT="/opt/centinela/letsencrypt"
ARCHIVO_FULLCHAIN_TLS="${DIRECTORIO_LETSENCRYPT}/live/${DOMINIO_SERVIDOR}/fullchain.pem"

# Nombres que viajan en el mismo certificado. www no sirve contenido, solo
# redirige al apex. api y staging son alias del MISMO contenedor de la API:
# staging y main comparten esta VM, así que entre ellos solo cambia la
# etiqueta de la imagen y no son ambientes aislados (ver README.md).
DOMINIOS_TLS=("${DOMINIO_SERVIDOR}" "www.${DOMINIO_SERVIDOR}" "api.${DOMINIO_SERVIDOR}" "staging.${DOMINIO_SERVIDOR}")

ARCHIVO_SERVICE_RENOVACION="/etc/systemd/system/centinela-certbot-renovar.service"
ARCHIVO_TIMER_RENOVACION="/etc/systemd/system/centinela-certbot-renovar.timer"

CERTBOT_STAGING="${CERTBOT_STAGING:-no}"

# Tres guards de salto, cada uno con su propio motivo. Ninguno es fatal:
# probar-en-docker.sh no define CORREO_CERTBOT, así que este paso se omite
# entero ahí y sigue reportando 0 cambios extra en la segunda corrida.
_certbot50_razon_omision=""

if [[ ! -d /run/systemd/system ]]; then
  _certbot50_razon_omision="systemd no es el proceso 1 en este sistema (no existe /run/systemd/system)"
elif [[ "${DOMINIO_SERVIDOR}" == "_" ]]; then
  _certbot50_razon_omision="DOMINIO_SERVIDOR sigue siendo el comodín _ (ver infra/servidor/dominio-fijado.sh): no tiene sentido emitir un certificado para él"
elif [[ -z "${CORREO_CERTBOT:-}" ]]; then
  _certbot50_razon_omision="falta definir CORREO_CERTBOT (ver infra/servidor/README.md, sección \"Configuración local\"): un correo no es una constante del proyecto, se lee de aprovisionar.env"
fi

if [[ -n "${_certbot50_razon_omision}" ]]; then
  aviso "Se omite la emisión del certificado TLS y su temporizador de renovación: ${_certbot50_razon_omision}. Verifícalo en la VM real (Azure o Vagrant), con aprovisionar.env configurado."
else
  if [[ "${CERTBOT_STAGING}" == "si" ]]; then
    aviso "CERTBOT_STAGING=si: se va a usar el entorno de pruebas de Let's Encrypt (certificado NO válido para navegadores). La existencia de ${ARCHIVO_FULLCHAIN_TLS} es lo único que decide si este paso reemite o no, y no distingue un certificado de staging de uno real: si ya existe uno de staging y quieres el real, borra a mano ${DIRECTORIO_LETSENCRYPT}/live/${DOMINIO_SERVIDOR}, ${DIRECTORIO_LETSENCRYPT}/archive/${DOMINIO_SERVIDOR} y ${DIRECTORIO_LETSENCRYPT}/renewal/${DOMINIO_SERVIDOR}.conf antes de volver a correr este paso sin CERTBOT_STAGING."
  fi

  asegurar_directorio "${DIRECTORIO_LETSENCRYPT}"
  # Por si este paso corre solo (--solo 50-certbot.sh) antes de que
  # 30-nginx.sh haya creado este directorio alguna vez.
  asegurar_directorio "${DIRECTORIO_WEBROOT}"

  # Un certificado puede existir y aun así no cubrir todos los nombres: la
  # primera emisión pedía solo el apex, y www, api y staging quedaban con
  # error de hostname. El predicado es "existe Y cubre www", no solo "existe".
  falta_dominio_tls="no"
  if [[ -f "${ARCHIVO_FULLCHAIN_TLS}" ]] && ! openssl x509 -in "${ARCHIVO_FULLCHAIN_TLS}" -noout -ext subjectAltName 2>/dev/null | grep -q "DNS:www.${DOMINIO_SERVIDOR}"; then
    falta_dominio_tls="si"
  fi

  if [[ -f "${ARCHIVO_FULLCHAIN_TLS}" && "${falta_dominio_tls}" == "no" ]]; then
    info "El certificado para ${DOMINIO_SERVIDOR} ya existe en ${ARCHIVO_FULLCHAIN_TLS} y cubre ${DOMINIOS_TLS[*]}: no se reemite. Igual que asegurar_archivo_con_secreto, es la existencia del archivo la que decide, no una variable de estado aparte."
  else
    # Contenedor efímero, no un servicio de compose de larga duración: el
    # plugin webroot solo necesita el puerto 80 ya servido por nginx (con
    # nginx/webroot montado sin :ro acá, a diferencia del mount de solo
    # lectura que compose.yaml le da a nginx sobre la misma ruta de host).
    comando_certonly=(certonly --webroot -w /var/www/certbot
      -m "${CORREO_CERTBOT}" --agree-tos --non-interactive)
    for dominio in "${DOMINIOS_TLS[@]}"; do
      comando_certonly+=(-d "${dominio}")
    done
    if [[ -f "${ARCHIVO_FULLCHAIN_TLS}" ]]; then
      # Certificado previo al que le faltan nombres: --expand lo reemplaza por
      # uno que los cubre todos sin gastar el cupo de certificados duplicados.
      comando_certonly+=(--expand)
    fi
    if [[ "${CERTBOT_STAGING}" == "si" ]]; then
      comando_certonly+=(--staging)
    fi

    info "Emitiendo el certificado para ${DOMINIOS_TLS[*]} (correo ${CORREO_CERTBOT}, imagen ${IMAGEN_CERTBOT}, staging=${CERTBOT_STAGING})."

    # Monta /etc/letsencrypt COMPLETO, no solo live/: live/ son symlinks
    # hacia archive/, y montar solo live/ rompe esos symlinks dentro del
    # contenedor (ver README.md).
    if ! docker run --rm \
      -v "${DIRECTORIO_WEBROOT}:/var/www/certbot" \
      -v "${DIRECTORIO_LETSENCRYPT}:/etc/letsencrypt" \
      "${IMAGEN_CERTBOT}" "${comando_certonly[@]}"; then
      error_fatal "certbot certonly falló al emitir el certificado para ${DOMINIOS_TLS[*]}. Revisa la salida de arriba antes de reintentar: Let's Encrypt real limita a 5 validaciones fallidas por hora y 5 certificados duplicados por semana (usa CERTBOT_STAGING=si para probar sin gastar ese límite)."
    fi

    if [[ ! -f "${ARCHIVO_FULLCHAIN_TLS}" ]]; then
      error_fatal "certbot certonly terminó sin error, pero ${ARCHIVO_FULLCHAIN_TLS} no existe. Revisa el volumen montado y que ${DOMINIOS_TLS[*]} resuelvan hacia este servidor."
    fi

    info "Certificado emitido: ${ARCHIVO_FULLCHAIN_TLS} existe."
    registrar_cambio
  fi

  # compose.certbot.yaml: agrega el puerto 443 y el volumen de certificados
  # al servicio nginx (dueño en compose.yaml, escrito por 30-nginx.sh). Sin
  # "name:" ni "networks:": esas dos claves siguen siendo exclusivas de
  # compose.yaml (ver README.md, sección de compose multi-archivo).
  contenido_compose_certbot="$(cat <<EOF
# infra/servidor/compose.certbot.yaml, generado por 50-certbot.sh, no
# versionado como archivo estático aparte.
services:
  nginx:
    ports:
      - "443:443"
    volumes:
      - ${DIRECTORIO_LETSENCRYPT}:/etc/letsencrypt:ro
EOF
  )"
  asegurar_archivo_con_contenido "${ARCHIVO_COMPOSE_CERTBOT}" "${contenido_compose_certbot}"

  # centinela-tls.conf: el bloque "listen 443 ssl", dueño exclusivo de este
  # paso. Incluye raiz-http.conf (propiedad de 30-nginx.sh) para el cuerpo
  # del proxy: el location / de HTTPS es el mismo proxy que el de HTTP, solo
  # cambia el listener. Rutas de certificado en la ruta DENTRO del
  # contenedor (/etc/letsencrypt, el punto de montaje), no la ruta de host
  # (${DIRECTORIO_LETSENCRYPT}).
  contenido_centinela_tls="$(cat <<EOF
# infra/servidor/nginx/conf.d/centinela-tls.conf (generado por 50-certbot.sh, no editar a mano)
#
# location / vive en snippets/raiz-http.conf, propiedad de 30-nginx.sh: este
# bloque solo agrega el listener 443 con el certificado ya emitido.
server {
    listen 443 ssl;
    http2 on;
    server_name ${DOMINIO_SERVIDOR};

    ssl_certificate /etc/letsencrypt/live/${DOMINIO_SERVIDOR}/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/${DOMINIO_SERVIDOR}/privkey.pem;

    include /etc/nginx/snippets/cabeceras-seguridad.conf;
    include /etc/nginx/snippets/raiz-http.conf;
}
EOF
  )"
  asegurar_archivo_con_contenido "${DIRECTORIO_NGINX}/conf.d/centinela-tls.conf" "${contenido_centinela_tls}"

  # cabeceras-seguridad.conf: se incluye a nivel de server en los vhosts TLS
  # de la API. Ninguno de sus locations declara add_header propias, así que
  # heredan estas. PSE tiene su propio HSTS en pse-tls.conf y no lo incluye.
  contenido_cabeceras="$(cat <<'EOF'
# infra/servidor/nginx/snippets/cabeceras-seguridad.conf (generado por 50-certbot.sh, no editar a mano)
add_header Strict-Transport-Security "max-age=31536000" always;
add_header X-Content-Type-Options "nosniff" always;
add_header Referrer-Policy "no-referrer" always;
EOF
  )"
  asegurar_archivo_con_contenido "${DIRECTORIO_NGINX}/snippets/cabeceras-seguridad.conf" "${contenido_cabeceras}"

  # www-tls.conf: www no sirve contenido. El cliente móvil usa el apex como
  # API_BASE_URL, así que el único trabajo de www es no romper TLS y mandar
  # al apex con un 301.
  contenido_www_tls="$(cat <<EOF
# infra/servidor/nginx/conf.d/www-tls.conf (generado por 50-certbot.sh, no editar a mano)
server {
    listen 443 ssl;
    http2 on;
    server_name www.${DOMINIO_SERVIDOR};

    ssl_certificate /etc/letsencrypt/live/${DOMINIO_SERVIDOR}/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/${DOMINIO_SERVIDOR}/privkey.pem;

    include /etc/nginx/snippets/cabeceras-seguridad.conf;
    return 301 https://${DOMINIO_SERVIDOR}\$request_uri;
}
EOF
  )"
  asegurar_archivo_con_contenido "${DIRECTORIO_NGINX}/conf.d/www-tls.conf" "${contenido_www_tls}"

  # api y staging: alias del mismo contenedor de la API, con el mismo proxy
  # que el apex (raiz-http.conf). staging y main comparten esta VM y lo único
  # que cambia entre ambos es la etiqueta de la imagen, así que estos dos
  # nombres apuntan al proceso que esté desplegado en ese momento.
  for nombre_alias in api staging; do
    contenido_alias_tls="$(cat <<EOF
# infra/servidor/nginx/conf.d/${nombre_alias}-tls.conf (generado por 50-certbot.sh, no editar a mano)
server {
    listen 443 ssl;
    http2 on;
    server_name ${nombre_alias}.${DOMINIO_SERVIDOR};

    ssl_certificate /etc/letsencrypt/live/${DOMINIO_SERVIDOR}/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/${DOMINIO_SERVIDOR}/privkey.pem;

    include /etc/nginx/snippets/cabeceras-seguridad.conf;
    include /etc/nginx/snippets/raiz-http.conf;
}
EOF
    )"
    asegurar_archivo_con_contenido "${DIRECTORIO_NGINX}/conf.d/${nombre_alias}-tls.conf" "${contenido_alias_tls}"
  done

  # raiz-redirigir.conf: la redirección a HTTPS que 30-nginx.sh incluye
  # desde centinela.conf en cuanto su propio predicado detecta el
  # certificado. NUNCA sobreescribe raiz-http.conf. Delimitador CON
  # comillas: $host y $request_uri son variables de nginx, no de bash.
  contenido_raiz_redirigir="$(cat <<'EOF'
# infra/servidor/nginx/snippets/raiz-redirigir.conf (generado por 50-certbot.sh, no editar a mano)
#
# Incluido desde conf.d/centinela.conf (30-nginx.sh) una vez que su
# predicado detecta el certificado emitido por este paso.
location / {
    return 301 https://$host$request_uri;
}
EOF
  )"
  asegurar_archivo_con_contenido "${DIRECTORIO_NGINX}/snippets/raiz-redirigir.conf" "${contenido_raiz_redirigir}"

  # Temporizador de renovación con systemd, NO con cron. OnBootSec+
  # OnUnitActiveSec (no OnCalendar=daily+Persistent=true): SCRUM-29 apaga la
  # VM a diario (Azure deallocate) y Persistent=true solo dispara una
  # corrida perdida si existe un stamp de una activación previa, cosa que no
  # hay la primera vez que el timer se activa después de un apagado a las
  # 00:00. OnBootSec+OnUnitActiveSec sí garantiza una corrida temprana en
  # cada encendido diario (ver README.md).
  contenido_service_renovacion="$(cat <<EOF
[Unit]
Description=Renovar el certificado TLS de Centinela con certbot (SCRUM-30)
After=docker.service
Requires=docker.service

[Service]
Type=oneshot
ExecStart=/usr/bin/docker run --rm -v ${DIRECTORIO_WEBROOT}:/var/www/certbot -v ${DIRECTORIO_LETSENCRYPT}:/etc/letsencrypt ${IMAGEN_CERTBOT} renew --webroot -w /var/www/certbot
ExecStart=/usr/bin/docker kill -s HUP centinela-nginx
EOF
  )"
  # Dos ExecStart en el mismo Service, no un "--deploy-hook" de certbot: el
  # hook de despliegue de certbot corre DENTRO de su propio contenedor
  # efímero, que no tiene acceso al socket de Docker, así que no puede
  # recargar nginx. El reload se dispara desde el host, después del "docker
  # run", apoyado en el comportamiento por defecto de systemd: si el primer
  # ExecStart falla, el segundo no se ejecuta (Type=oneshot). "certbot renew"
  # sin certificados vencidos igual sale con código 0, así que este segundo
  # ExecStart corre en CADA disparo del timer que no falle, no solo cuando
  # de verdad renovó algo (ver README.md, "Qué queda abierto").
  contenido_timer_renovacion="$(cat <<'EOF'
[Unit]
Description=Disparar la renovación diaria del certificado TLS de Centinela (SCRUM-30)

[Timer]
OnBootSec=15min
OnUnitActiveSec=24h

[Install]
WantedBy=timers.target
EOF
  )"

  cambios_antes_systemd="$(resumen_cambios)"
  asegurar_archivo_con_contenido "${ARCHIVO_SERVICE_RENOVACION}" "${contenido_service_renovacion}"
  asegurar_archivo_con_contenido "${ARCHIVO_TIMER_RENOVACION}" "${contenido_timer_renovacion}"
  cambios_despues_systemd="$(resumen_cambios)"

  if [[ "${cambios_despues_systemd}" -gt "${cambios_antes_systemd}" ]]; then
    info "Las unidades systemd del temporizador de renovación cambiaron: se recarga el daemon y se habilita."
    systemctl daemon-reload
    systemctl enable --now centinela-certbot-renovar.timer
  else
    info "Las unidades systemd del temporizador de renovación ya estaban en el estado esperado: no se recarga el daemon."
  fi

  # Defensa adicional, igual patrón que 10-ssh.sh: por si el contenido ya
  # estaba correcto pero el timer no había quedado habilitado o activo.
  asegurar_servicio_activo centinela-certbot-renovar.timer
fi
