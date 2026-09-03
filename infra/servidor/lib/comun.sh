#!/usr/bin/env bash
# infra/servidor/lib/comun.sh
#
# Helpers de idempotencia para el orquestador de aprovisionamiento. Toda función
# que empieza con "asegurar_" o "instalar_" sigue la misma regla: solo llama a
# registrar_cambio cuando de verdad modificó el estado del sistema. Si el estado
# ya era el esperado, lo informa con info y no cuenta cambio. Esa es la prueba
# observable de idempotencia: ejecutar el script dos veces seguidas tiene que
# reportar 0 cambios en la segunda corrida.
#
# Este archivo se carga con source desde aprovisionar.sh, nunca se ejecuta como
# proceso aparte.

# Contador global de cambios aplicados durante la corrida actual.
_APROV_CAMBIOS=0

# Contador global de pasos que se saltaron por estar pendientes de implementar.
_APROV_PASOS_PENDIENTES=0

# Bandera interna: si "apt-get update" ya se ejecutó en esta corrida.
_APROV_APT_ACTUALIZADO="no"

paso() {
  printf '==> %s\n' "$1"
}

info() {
  printf '    %s\n' "$1"
}

aviso() {
  # A stdout, igual que paso() e info(): así el log queda en orden cronológico
  # cuando la salida se redirige a un archivo. error_fatal() sí se queda en
  # stderr porque marca el corte definitivo de la corrida.
  printf '[aviso] %s\n' "$1"
}

error_fatal() {
  printf '[error] %s\n' "$1" >&2
  exit 1
}

exigir_root() {
  if [[ "${EUID}" -ne 0 ]]; then
    error_fatal "Este script se debe ejecutar como root (o con sudo)."
  fi
}

exigir_variable() {
  local nombre="$1"
  if [[ -z "${!nombre:-}" ]]; then
    error_fatal "Falta definir la variable de entorno ${nombre}."
  fi
}

paquete_instalado() {
  local nombre="$1"
  dpkg -s "$nombre" &>/dev/null
}

instalar_paquetes() {
  local pendientes=()
  local paquete

  for paquete in "$@"; do
    if paquete_instalado "$paquete"; then
      info "El paquete ${paquete} ya está instalado."
    else
      pendientes+=("$paquete")
    fi
  done

  if [[ "${#pendientes[@]}" -eq 0 ]]; then
    return 0
  fi

  if [[ "${_APROV_APT_ACTUALIZADO}" != "si" ]]; then
    info "Actualizando el índice de paquetes (apt-get update)."
    DEBIAN_FRONTEND=noninteractive apt-get update -y
    _APROV_APT_ACTUALIZADO="si"
  fi

  info "Instalando: ${pendientes[*]}"
  DEBIAN_FRONTEND=noninteractive apt-get install -y "${pendientes[@]}"

  for paquete in "${pendientes[@]}"; do
    registrar_cambio
  done
}

asegurar_linea_en_archivo() {
  local archivo="$1"
  local linea="$2"

  if [[ ! -f "$archivo" ]]; then
    : > "$archivo"
  fi

  if grep -qxF "$linea" "$archivo"; then
    info "La línea ya está presente en ${archivo}."
    return 0
  fi

  printf '%s\n' "$linea" >> "$archivo"
  registrar_cambio
}

asegurar_directorio() {
  local ruta="$1"
  local modo="${2:-}"
  local propietario="${3:-}"
  local cambiado="no"

  if [[ ! -d "$ruta" ]]; then
    mkdir -p "$ruta"
    cambiado="si"
  fi

  if [[ -n "$modo" ]]; then
    local modo_actual
    modo_actual="$(stat -c '%a' "$ruta")"
    if [[ "$modo_actual" != "$modo" ]]; then
      chmod "$modo" "$ruta"
      cambiado="si"
    fi
  fi

  if [[ -n "$propietario" ]]; then
    local dueno_actual
    dueno_actual="$(stat -c '%U:%G' "$ruta")"
    if [[ "$dueno_actual" != "$propietario" ]]; then
      chown "$propietario" "$ruta"
      cambiado="si"
    fi
  fi

  if [[ "$cambiado" == "si" ]]; then
    registrar_cambio
  else
    info "El directorio ${ruta} ya estaba en el estado esperado."
  fi
}

asegurar_servicio_activo() {
  local servicio="$1"
  local habilitado="no"
  local activo="no"

  # No se usa "command -v systemctl" para decidir si hay servicio que tocar:
  # ese binario puede quedar instalado como dependencia de otro paquete (por
  # ejemplo docker.io, que trae systemd y systemd-sysv) sin que systemd sea
  # de verdad el proceso 1, como pasa en el contenedor de
  # infra/servidor/probar-en-docker.sh. Ahí "systemctl enable --now" se
  # niega con "--now cannot be used when systemd is not running", lo que
  # aborta toda la corrida. /run/systemd/system solo existe cuando systemd
  # es el proceso 1: es la misma comprobación que usa sd_booted() de
  # libsystemd.
  if [[ ! -d /run/systemd/system ]]; then
    aviso "systemd no es el proceso 1 en este sistema (no existe /run/systemd/system), se omite ${servicio}."
    return 0
  fi

  if systemctl is-enabled --quiet "$servicio" 2>/dev/null; then
    habilitado="si"
  fi

  if systemctl is-active --quiet "$servicio" 2>/dev/null; then
    activo="si"
  fi

  if [[ "$habilitado" == "si" && "$activo" == "si" ]]; then
    info "El servicio ${servicio} ya está habilitado y activo."
    return 0
  fi

  systemctl enable --now "$servicio"
  registrar_cambio
}

registrar_cambio() {
  _APROV_CAMBIOS=$((_APROV_CAMBIOS + 1))
}

resumen_cambios() {
  printf '%s\n' "${_APROV_CAMBIOS}"
}

# Helper de apoyo para los pasos que todavía no están implementados (esqueleto).
# Deja constancia clara de que el paso no aplicó ningún cambio y lo cuenta como
# pendiente en el resumen final, sin hacer fallar la corrida.
paso_pendiente() {
  local tarjeta="$1"
  aviso "Paso pendiente de implementar (tarjeta ${tarjeta}). No se aplicó ningún cambio."
  _APROV_PASOS_PENDIENTES=$((_APROV_PASOS_PENDIENTES + 1))
}

asegurar_archivo_con_contenido() {
  local ruta="$1"
  local contenido="$2"

  if [[ -f "$ruta" ]] && [[ "$(cat "$ruta")" == "$contenido" ]]; then
    info "El archivo ${ruta} ya tiene el contenido esperado."
    return 0
  fi

  asegurar_directorio "$(dirname "$ruta")"
  printf '%s\n' "$contenido" > "$ruta"
  registrar_cambio
}

asegurar_servicio_compose() {
  local archivo_compose="$1"
  local servicio="$2"

  # Mismo criterio que asegurar_servicio_activo: sin systemd como proceso 1
  # no hay daemon de Docker que alcanzar (docker.io nunca llegó a arrancar
  # su servicio), así que no tiene sentido intentar "docker compose up".
  # Esto es lo que hace que este helper se salte por completo dentro de
  # infra/servidor/probar-en-docker.sh (ese contenedor de prueba tampoco
  # tiene acceso al socket de Docker, así que aunque systemd sí fuera el
  # proceso 1 ahí, esto seguiría sin poder correr; en la práctica ninguna
  # de las dos condiciones se cumple dentro de esa prueba).
  if [[ ! -d /run/systemd/system ]]; then
    aviso "systemd no es el proceso 1 en este sistema (no existe /run/systemd/system), se omite el contenedor ${servicio}."
    return 0
  fi

  # "$archivo_compose" ya NO se usa como "el" archivo de compose: desde que
  # Postgres (40-postgres.sh, SCRUM-33) empezó a declarar su propio
  # compose.postgres.yaml en vez de compartir compose.yaml con nginx
  # (30-nginx.sh), este helper necesita fusionar TODOS los fragmentos del
  # directorio, no solo el que recibió como argumento. Se usa el argumento
  # únicamente para derivar el directorio con dirname, y de ahí se arma un
  # arreglo de banderas "-f <archivo>" con cada compose*.yaml que exista ahí
  # (mismo patrón de glob con "[[ -f ... ]]" filtrando que ya usa
  # mostrar_lista() en aprovisionar.sh para listar pasos/NN-*.sh). docker
  # compose fusiona las claves de nivel superior (name, networks, services)
  # entre todos los "-f" que reciba en una misma invocación, así que pasar
  # todos los fragmentos encontrados reproduce el mismo resultado que un
  # único compose.yaml grande, sin que un paso tenga que reescribir el
  # bloque "services:" de otro. Cuando compose.postgres.yaml todavía no
  # existe (por ejemplo, la primera vez que corre 30-nginx.sh en una
  # instalación nueva), el glob solo encuentra compose.yaml, así que el
  # comportamiento para ese call site queda idéntico al de antes de este
  # cambio.
  local directorio archivo
  local -a banderas_compose=()
  directorio="$(dirname "$archivo_compose")"
  for archivo in "${directorio}"/compose*.yaml; do
    [[ -f "$archivo" ]] || continue
    banderas_compose+=(-f "$archivo")
  done

  if [[ "${#banderas_compose[@]}" -eq 0 ]]; then
    error_fatal "No se encontró ningún archivo compose*.yaml en ${directorio}."
  fi

  # No se usa un chequeo previo de "¿ya está corriendo?" para decidir si
  # llamar "docker compose up -d": eso dejaría pasar sin aplicar un cambio
  # de configuración (imagen, volúmenes, red) en un servicio que ya estaba
  # corriendo con la versión VIEJA de compose.yaml. "docker compose up -d"
  # ya es idempotente por diseño (no hace nada si nada cambió, recrea si
  # algo cambió); lo único que hace falta es medir si de verdad hizo algo,
  # comparando el ID del contenedor y su estado antes y después.
  local id_antes id_despues estado_antes
  id_antes="$(docker compose "${banderas_compose[@]}" ps -q "$servicio" 2>/dev/null || true)"
  estado_antes="inexistente"
  if [[ -n "$id_antes" ]]; then
    estado_antes="$(docker inspect -f '{{.State.Status}}' "$id_antes" 2>/dev/null || echo "inexistente")"
  fi

  docker compose "${banderas_compose[@]}" up -d "$servicio"

  id_despues="$(docker compose "${banderas_compose[@]}" ps -q "$servicio" 2>/dev/null || true)"

  if [[ -z "$id_despues" ]]; then
    error_fatal "docker compose up -d ${servicio} no dejó ningún contenedor corriendo."
  fi

  if [[ "$id_despues" != "$id_antes" || "$estado_antes" != "running" ]]; then
    info "El contenedor ${servicio} se creó o se recreó."
    registrar_cambio
  else
    info "El contenedor ${servicio} ya estaba corriendo con la configuración actual."
  fi
}

# Genera una cadena de 32 caracteres alfanuméricos aptos para usarse sin
# escapar dentro de un archivo de entorno o en una URL de conexión sin
# codificar: nunca contiene "/", "+" ni "=", los tres caracteres que sí
# puede producir el alfabeto estándar de base64.
#
# Se piden 128 bytes de /dev/urandom, no 24 (que darían justo 32 caracteres
# de base64 antes de filtrar): al convertir a base64 y descartar los
# caracteres fuera de [A-Za-z0-9] (unos 3 de cada 64 símbolos del alfabeto
# caen), sobra margen de sobra para quedarse con los primeros 32 caracteres
# alfanuméricos sin tener que repetir la generación ni comprobar cuántos
# sobrevivieron al filtro.
#
# El recorte a 32 caracteres se hace con expansión de parámetros de bash
# ("${crudo:0:32}"), no con un "head -c 32" al final de la tubería: cortar
# ahí cerraría la entrada de "tr" antes de que "base64" (y, más arriba,
# "head -c 128 /dev/urandom") terminaran de escribir toda su salida, lo que
# manda SIGPIPE al proceso anterior. aprovisionar.sh declara
# "set -euo pipefail", y ese "pipefail" se hereda en todo lo que se carga
# con source (incluido este archivo): bajo esa opción, un SIGPIPE a mitad
# de tubería vuelve fallido el código de salida de la tubería completa,
# aunque la cadena generada fuera perfectamente válida. Truncar con
# expansión de parámetros, después de que la tubería ya terminó de correr
# completa, evita ese fallo intermitente.
#
# "/dev/urandom" en vez de "$RANDOM" de bash: "$RANDOM" es un generador
# pseudoaleatorio simple, no pensado para secretos. "base64" (paquete
# coreutils) y "/dev/urandom" (expuesto por el kernel) no dependen de un
# paquete extra como "openssl", que podría faltar en la imagen mínima que
# instala 20-docker.sh con --no-install-recommends. Verificado el 3 de
# septiembre de 2026 con "docker run --rm debian:13 which base64" y
# "docker run --rm debian:13 test -e /dev/urandom": los dos existen en esa
# imagen.
generar_secreto() {
  local crudo

  crudo="$(head -c 128 /dev/urandom | base64 | tr -dc 'A-Za-z0-9')"

  if [[ "${#crudo}" -lt 32 ]]; then
    error_fatal "No se pudieron generar 32 caracteres alfanuméricos a partir de /dev/urandom."
  fi

  printf '%s' "${crudo:0:32}"
}

# Crea "$ruta" con una única línea "$nombre_variable=<secreto de 32
# caracteres>" la primera vez que se llama, y no hace nada si el archivo ya
# existe. Ese "si ya existe, no tocarlo" es lo que vuelve idempotente este
# helper por diseño, no por comparación de contenido: a diferencia de
# asegurar_archivo_con_contenido, que reescribe el archivo cuando su
# contenido no coincide con el esperado, acá el contenido esperado no se
# puede recalcular (es aleatorio), así que la única pregunta posible es "el
# archivo ya existe", nunca "el secreto que tiene es el correcto".
#
# El valor que queda en "$ruta" solo importa en el primer arranque del
# contenedor que lo consuma con "env_file:": la imagen oficial de Postgres
# usa esa variable para inicializar el rol y la base con "initdb" la
# primera vez que ve el directorio de datos vacío. Editar el archivo
# después de ese primer arranque no cambia nada en la base ya inicializada,
# la contraseña quedó grabada dentro de Postgres, no en este archivo.
# Rotarla de verdad requiere "ALTER ROLE ... WITH PASSWORD ..." dentro de
# la base, y recién después actualizar este archivo para que vuelva a
# coincidir.
asegurar_archivo_con_secreto() {
  local ruta="$1"
  local nombre_variable="$2"
  local secreto

  if [[ -f "$ruta" ]]; then
    info "El archivo ${ruta} ya existe: no se regenera ${nombre_variable}."
    return 0
  fi

  # El secreto se captura en una variable propia, no se interpola
  # "$(generar_secreto)" directo dentro del "printf" de más abajo: un
  # "error_fatal" (exit 1) dentro de una sustitución de comando que forma
  # parte de los argumentos de otro comando no dispara "set -e" (el código
  # de salida que "set -e" ve es el del comando completo, "printf", que
  # sigue siendo 0 aunque la sustitución interna haya fallado). Como
  # asignación simple ("secreto=..."), en cambio, el código de salida de la
  # sustitución sí se propaga, así que un fallo de generar_secreto corta acá
  # con "set -e" antes de escribir nada. El chequeo de longitud explícito de
  # abajo es una segunda red, no la única: cubre el caso (hoy inalcanzable
  # en la práctica, ver generar_secreto) de que la sustitución haya
  # devuelto éxito con una cadena más corta de lo esperado.
  secreto="$(generar_secreto)"
  if [[ "${#secreto}" -ne 32 ]]; then
    error_fatal "generar_secreto no devolvió una cadena de 32 caracteres para ${ruta}."
  fi

  asegurar_directorio "$(dirname "$ruta")" "700"
  printf '%s=%s\n' "$nombre_variable" "$secreto" > "$ruta"
  chmod 600 "$ruta"
  registrar_cambio
}
