#!/usr/bin/env bash
# infra/azure/crear-servidor.sh
#
# Consumidor de infra/azure/variables.sh (tarjeta SCRUM-26). Crea en Azure
# los recursos explícitos del servidor real de Centinela: grupo de recursos,
# red virtual, grupo de seguridad de red con sus reglas, IP pública, tarjeta
# de red y la máquina virtual, en ese orden.
#
# Corre en el portátil del operador, no en el servidor: infra/azure/ está al
# lado de infra/servidor/ que sí viaja en el despliegue (ver infra/servidor/README.md
# e infra/azure/variables.sh), pero este archivo se queda de este lado de la
# frontera de despliegue.
#
# Por qué este script NO hace source de infra/servidor/lib/comun.sh, aunque
# ese archivo ya resuelve paso/info/aviso/error_fatal y un contador de
# cambios: sus otros helpers asumen que corren dentro del servidor real como
# root. exigir_root corta si EUID != 0, y este script corre como el usuario
# normal del operador en su portátil. instalar_paquetes usa dpkg y apt-get,
# que no tienen sentido acá. registrar_cambio y resumen_cambios mutan un
# contador pensado para el lado del servidor: compartirlo cruzaría la
# frontera de despliegue con estado, no solo con configuración, que es
# justo lo que infra/azure/variables.sh evita al hacer source de
# infra/servidor/imagen-fijada.sh en vez de duplicarlo. Por eso los cuatro
# helpers de presentación (paso, info, aviso, error_fatal) y un contador de
# cambios propio se copian aquí abajo: es la misma forma que lib/comun.sh
# pero un archivo con un propósito distinto, no una duplicación descuidada.
#
# Banderas: --simular (nueva de este script, no existe en
# infra/servidor/aprovisionar.sh, cuyas banderas son --lista, --solo y
# --ayuda), --registrar-proveedores y --ayuda. Ver mostrar_ayuda más abajo
# para el detalle de cada una.
#
# Este script NO incluye ningún camino de rollback ni de teardown: está
# fuera del alcance de la tarjeta SCRUM-26, y un teardown a medio escribir es
# más peligroso que ninguno. Borrar recursos en la nube necesita que lo
# decida un humano, no un script.
set -euo pipefail

# --- Helpers de presentación, copiados de infra/servidor/lib/comun.sh ---
# Ver la nota de cabecera de arriba: la copia es intencional, no un olvido.

paso() {
  printf '==> %s\n' "$1"
}

info() {
  printf '    %s\n' "$1"
}

aviso() {
  printf '[aviso] %s\n' "$1"
}

error_fatal() {
  printf '[error] %s\n' "$1" >&2
  exit 1
}

# Contador de cambios propio de este script, independiente del contador de
# infra/servidor/lib/comun.sh (ver la nota de cabecera). Los dos arreglos de
# abajo acompañan al contador para que el resumen final pueda nombrar los
# recursos, no solo decir cuántos: "3 cambios" no le dice a un humano si el
# tercero fue la VM o una regla del NSG.
_CREAR_CAMBIOS=0
RECURSOS_CREADOS=()
RECURSOS_EXISTENTES=()

registrar_cambio() {
  _CREAR_CAMBIOS=$((_CREAR_CAMBIOS + 1))
}

resumen_cambios() {
  printf '%s\n' "${_CREAR_CAMBIOS}"
}

# Registra un recurso que ya existía (se llama desde cada asegurar_* antes
# de su "return 0" temprano), para que el resumen final liste tanto lo
# creado como lo que ya estaba, no solo el conteo de cambios.
registrar_existente() {
  info "$1 ya existe."
  RECURSOS_EXISTENTES+=("$1")
}

DIRECTORIO_SCRIPT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Resuelto con BASH_SOURCE, no con una ruta relativa al directorio de
# trabajo, para que este script funcione sin importar desde dónde se invoque
# (igual que infra/azure/variables.sh resuelve infra/servidor/imagen-fijada.sh).
# shellcheck source=variables.sh
source "${DIRECTORIO_SCRIPT}/variables.sh"

SIMULAR="no"
REGISTRAR_PROVEEDORES="no"

mostrar_ayuda() {
  cat <<'EOF'
Uso: crear-servidor.sh [opciones]

Crea en Azure los recursos explícitos del servidor de Centinela (tarjeta
SCRUM-26): grupo de recursos, red virtual, grupo de seguridad de red con sus
reglas, IP pública, tarjeta de red y la máquina virtual. Lee su
configuración de infra/azure/variables.sh.

Opciones:
  --simular                 Imprime lo que haría, sin ejecutar ningún "az"
                             que escriba. Las lecturas (az account show, az
                             provider show, "az ... show", az group exists)
                             sí corren, para que la simulación refleje el
                             estado real de la suscripción. Bandera nueva de
                             este script: no existe en
                             infra/servidor/aprovisionar.sh, cuyas banderas
                             son --lista, --solo y --ayuda.
  --registrar-proveedores   Registra explícitamente los proveedores de
                             recurso obligatorios (Microsoft.Compute,
                             Microsoft.Network) que falten. Nunca se
                             registran de forma automática sin esta bandera:
                             registrar un proveedor es un cambio a nivel de
                             suscripción, no del grupo de recursos, y esa
                             suscripción la comparte el equipo.
  --ayuda                    Muestra esta ayuda.

Sin opciones, corre la verificación previa y crea los recursos que falten.

Variables de entorno relevantes:
  CONFIRMAR_SUSCRIPCION=si  Salta la confirmación interactiva de la
                             suscripción activa. Úsala solo si ya la
                             verificaste por otro medio (por ejemplo, en un
                             pipeline sin terminal).
  ORIGEN_SSH                Rango CIDR o IP individual que puede conectarse
                             al puerto 22 (SSH). Obligatoria: debe estar
                             definida, y no puede valer "*" ni "0.0.0.0/0".
                             Salida de escape: PERMITIR_SSH_ABIERTO=si.
  PERMITIR_SSH_ABIERTO=si   Deja el puerto 22 abierto a todo internet (solo
                             para casos muy concretos). Requiere aprobación
                             explícita en cada ejecución: no hay valor por
                             defecto.

Requiere "az login" vigente, incluso en modo --simular: la simulación lee
el estado real de la suscripción para ser fiel a lo que pasaría.

No incluye ningún camino de rollback ni de teardown: borrar recursos en la
nube necesita que lo decida un humano.
EOF
}

# Ejecuta "az $@" en modo lectura y traduce el código de salida a existe/no
# existe, sin volcar nada a stdout ni stderr: cada asegurar_* solo tiene que
# ramificar por el resultado, igual que el paso 00-verificar-sistema.sh de
# infra/servidor/ ramifica por sus propios chequeos.
recurso_existe() {
  az "$@" -o none --only-show-errors >/dev/null 2>&1
}

# Envoltorio único de escritura: en --simular imprime el comando en vez de
# ejecutarlo, y de cualquier forma cuenta el cambio, para que el resumen
# final reporte "cambios que se aplicarían" con el mismo número que
# aplicaría una corrida real. Fuera de --simular, revisa el código de salida
# y corta con error_fatal nombrando el recurso, no el volcado crudo del CLI.
ejecutar_cambio() {
  local descripcion="$1"
  shift

  if [[ "$SIMULAR" == "si" ]]; then
    info "[simular] Se ejecutaría: $*"
    registrar_cambio
    RECURSOS_CREADOS+=("$descripcion")
    return 0
  fi

  if ! "$@" -o none --only-show-errors; then
    error_fatal "No se pudo crear ${descripcion}."
  fi
  registrar_cambio
  RECURSOS_CREADOS+=("$descripcion")
}

verificar_cli_az() {
  command -v az >/dev/null 2>&1 \
    || error_fatal "No se encontró el CLI de az en el PATH. Instálalo y ejecuta 'az login' antes de continuar."
}

# Imprime la suscripción activa y exige confirmación interactiva antes de
# seguir: crear recursos en la suscripción equivocada es el error caro de
# esta tarjeta, y a diferencia de un recurso mal nombrado no se corrige con
# un rerun idempotente.
confirmar_suscripcion() {
  local salida respuesta
  local -a campos
  local nombre id

  salida="$(az account show --query "[name,id]" -o tsv --only-show-errors)" \
    || error_fatal "No se pudo consultar la suscripción activa de Azure. Verifica que 'az login' esté vigente."
  # El formato tsv de az emite una línea por campo, no campos separados por
  # tabulador en una sola línea, así que mapfile lee cada línea en un elemento
  # del arreglo en vez de dejar vacíos los campos posteriores al primero.
  mapfile -t campos <<<"$salida"
  nombre="${campos[0]}"
  id="${campos[1]}"

  info "Suscripción activa: ${nombre} (${id})"

  if [[ "$SIMULAR" == "si" ]]; then
    info "Modo --simular: no se va a crear nada, se omite la confirmación interactiva."
    return 0
  fi

  if [[ "${CONFIRMAR_SUSCRIPCION:-}" == "si" ]]; then
    info "CONFIRMAR_SUSCRIPCION=si: se omite la confirmación interactiva."
    return 0
  fi

  [[ -t 0 ]] \
    || error_fatal "No hay terminal interactiva para confirmar la suscripción. Define CONFIRMAR_SUSCRIPCION=si si ya la verificaste por otro medio."

  read -r -p "¿Es esta la suscripción correcta para crear el servidor? Escribe 's' o 'si' para continuar: " respuesta
  case "${respuesta,,}" in
    s | si | sí) ;;
    *) error_fatal "Confirmación no recibida: se detiene antes de crear ningún recurso." ;;
  esac
}

# Verifica un proveedor de recurso contra el registro de la suscripción.
# "obligatorio" en "si" corta con error_fatal si no está Registered (o lo
# registra, bajo --registrar-proveedores); en "no" solo avisa. Microsoft.Storage
# entra como no obligatorio: hoy nada lo necesita (los discos son
# administrados y van bajo Compute, y el diagnóstico de arranque
# administrado no usa cuenta de almacenamiento), pero se reporta en vez de
# callarlo, porque va a importar cuando el respaldo de la base se vaya a
# Blob.
verificar_proveedor() {
  local proveedor="$1" obligatorio="$2" estado

  estado="$(az provider show -n "$proveedor" --query registrationState -o tsv --only-show-errors 2>/dev/null || true)"

  if [[ "$estado" == "Registered" ]]; then
    info "Proveedor ${proveedor} ya está registrado."
    return 0
  fi

  if [[ "$obligatorio" == "si" && "$REGISTRAR_PROVEEDORES" == "si" ]]; then
    # --wait: el registro es asíncrono y queda en "Registering" varios
    # minutos. Sin esperar acá, el resto del script seguiría con el
    # proveedor todavía sin quedar Registered.
    ejecutar_cambio "el registro del proveedor ${proveedor}" \
      az provider register -n "$proveedor" --wait
    return 0
  fi

  if [[ "$obligatorio" == "si" ]]; then
    error_fatal "El proveedor ${proveedor} no está registrado (estado: ${estado:-desconocido}). Regístralo con: az provider register -n ${proveedor} --wait"
  fi

  aviso "El proveedor ${proveedor} no está registrado (estado: ${estado:-desconocido}). Hoy nada lo necesita, pero va a importar cuando el respaldo de la base se vaya a Blob. Regístralo con: az provider register -n ${proveedor} --wait"
}

# Deja las rutas de llave ya separadas en un arreglo global, para que
# asegurar_vm no tenga que repetir el mismo "read -ra" sobre
# LLAVES_SSH_PUBLICAS.
verificar_llaves_ssh() {
  local llave
  read -ra LLAVES_SSH_ARRAY <<<"$LLAVES_SSH_PUBLICAS"
  for llave in "${LLAVES_SSH_ARRAY[@]}"; do
    [[ -r "$llave" ]] || error_fatal "No se puede leer el archivo de llave pública SSH: ${llave}"
  done
}

# Verifica que ORIGEN_SSH esté definido y no sea un valor equivalente a
# abierto. Se prefiere fallar sobre advertir: un default abierto (sin
# fricción) termina en producción cuando alguien tiene prisa, y la regla del
# NSG es lo único que separa el puerto 22 de internet en una IP pública de
# SKU Standard sin otra protección de entrada.
verificar_origen_ssh() {
  if [[ "${PERMITIR_SSH_ABIERTO:-}" == "si" ]]; then
    aviso "PERMITIR_SSH_ABIERTO=si: el puerto 22 quedará expuesto a todo internet A PROPÓSITO."
    if [[ -z "$ORIGEN_SSH" ]]; then
      ORIGEN_SSH="*"
    fi
    return 0
  fi

  if [[ -z "$ORIGEN_SSH" ]]; then
    error_fatal "ORIGEN_SSH está vacía. Obtén tu IP pública con 'curl -fsS https://ifconfig.me' y ejecuta: ORIGEN_SSH=\$(curl -fsS https://ifconfig.me) bash infra/azure/crear-servidor.sh (o usa PERMITIR_SSH_ABIERTO=si como salida de escape deliberada)."
  fi

  local origen_lower="${ORIGEN_SSH,,}"
  case "$origen_lower" in
    \* | 0.0.0.0/0 | internet | any)
      error_fatal "ORIGEN_SSH=${ORIGEN_SSH} deja el puerto 22 abierto a todo internet. Obtén tu IP pública con 'curl -fsS https://ifconfig.me' y ejecuta: ORIGEN_SSH=\$(curl -fsS https://ifconfig.me) bash infra/azure/crear-servidor.sh (o usa PERMITIR_SSH_ABIERTO=si como salida de escape deliberada)."
      ;;
  esac

  info "ORIGEN_SSH se configurará como: ${ORIGEN_SSH}"
}

verificar_preflight() {
  paso "Verificación previa"
  verificar_cli_az
  confirmar_suscripcion
  verificar_proveedor "Microsoft.Compute" "si"
  verificar_proveedor "Microsoft.Network" "si"
  verificar_proveedor "Microsoft.Storage" "no"
  verificar_llaves_ssh
  verificar_origen_ssh
}

asegurar_grupo_recursos() {
  paso "Grupo de recursos: ${GRUPO_RECURSOS}"
  if [[ "$(az group exists -n "$GRUPO_RECURSOS" --only-show-errors)" == "true" ]]; then
    registrar_existente "El grupo de recursos ${GRUPO_RECURSOS}"
    return 0
  fi
  ejecutar_cambio "el grupo de recursos ${GRUPO_RECURSOS}" \
    az group create -n "$GRUPO_RECURSOS" -l "$REGION"
}

# No se declara un rango de direcciones propio: se deja el default del CLI
# (10.0.0.0/16 para la red, 10.0.0.0/24 para la subred). El plan no necesita
# hoy nada más específico, y agregar variables sin un uso real solo
# complicaría infra/azure/variables.sh.
asegurar_red_virtual() {
  paso "Red virtual: ${NOMBRE_RED_VIRTUAL}"
  if recurso_existe network vnet show -g "$GRUPO_RECURSOS" -n "$NOMBRE_RED_VIRTUAL"; then
    registrar_existente "La red virtual ${NOMBRE_RED_VIRTUAL}"
    return 0
  fi
  ejecutar_cambio "la red virtual ${NOMBRE_RED_VIRTUAL}" \
    az network vnet create -g "$GRUPO_RECURSOS" -n "$NOMBRE_RED_VIRTUAL" -l "$REGION" \
    --subnet-name "$NOMBRE_SUBRED"
}

asegurar_nsg() {
  paso "Grupo de seguridad de red: ${NOMBRE_GRUPO_SEGURIDAD}"
  if recurso_existe network nsg show -g "$GRUPO_RECURSOS" -n "$NOMBRE_GRUPO_SEGURIDAD"; then
    registrar_existente "El grupo de seguridad ${NOMBRE_GRUPO_SEGURIDAD}"
    return 0
  fi
  ejecutar_cambio "el grupo de seguridad ${NOMBRE_GRUPO_SEGURIDAD}" \
    az network nsg create -g "$GRUPO_RECURSOS" -n "$NOMBRE_GRUPO_SEGURIDAD" -l "$REGION"
}

# Imprime los valores actuales de la regla existente (origen, puerto,
# prioridad) para que un humano pueda comparar contra lo que se esperaba.
# Aceptar en silencio una regla de SSH con el origen equivocado es una falla
# de seguridad, no cosmética.
mostrar_aviso_regla_existente() {
  local nombre="$1" salida
  local -a campos
  local origen_actual puerto_actual prioridad_actual

  salida="$(az network nsg rule show --nsg-name "$NOMBRE_GRUPO_SEGURIDAD" -g "$GRUPO_RECURSOS" -n "$nombre" \
    --query "[sourceAddressPrefix,destinationPortRange,priority]" -o tsv --only-show-errors)" \
    || error_fatal "No se pudo leer el detalle de la regla NSG ${nombre}."
  # El formato tsv de az emite una línea por campo, no campos separados por
  # tabulador en una sola línea, así que mapfile lee cada línea en un elemento
  # del arreglo en vez de dejar vacíos los campos posteriores al primero.
  mapfile -t campos <<<"$salida"
  origen_actual="${campos[0]}"
  puerto_actual="${campos[1]}"
  prioridad_actual="${campos[2]}"

  aviso "La regla NSG ${nombre} ya existe con origen=${origen_actual}, puerto=${puerto_actual}, prioridad=${prioridad_actual}. No se edita en su lugar: una regla puede existir con el mismo nombre pero otro puerto, otro origen u otra prioridad, y eso lo tiene que revisar una persona."
}

# Política del NSG: la existencia de la regla se chequea por nombre, y esa
# existencia basta para no volver a crearla. NUNCA se edita una regla en su
# lugar (ver mostrar_aviso_regla_existente): si el equipo necesita otro
# origen o prioridad, se borra y se recrea a mano, a propósito, no desde
# este script.
asegurar_regla_nsg() {
  local nombre="$1" prioridad="$2" puerto="$3" origen="$4"

  if recurso_existe network nsg rule show --nsg-name "$NOMBRE_GRUPO_SEGURIDAD" -g "$GRUPO_RECURSOS" -n "$nombre"; then
    registrar_existente "La regla NSG ${nombre}"
    mostrar_aviso_regla_existente "$nombre"
    return 0
  fi

  ejecutar_cambio "la regla NSG ${nombre}" \
    az network nsg rule create --nsg-name "$NOMBRE_GRUPO_SEGURIDAD" -g "$GRUPO_RECURSOS" \
    -n "$nombre" --priority "$prioridad" --destination-port-ranges "$puerto" \
    --source-address-prefixes "$origen" --access Allow --protocol Tcp --direction Inbound
}

asegurar_ip_publica() {
  paso "IP pública: ${NOMBRE_IP_PUBLICA}"
  if recurso_existe network public-ip show -g "$GRUPO_RECURSOS" -n "$NOMBRE_IP_PUBLICA"; then
    registrar_existente "La IP pública ${NOMBRE_IP_PUBLICA}"
    return 0
  fi
  ejecutar_cambio "la IP pública ${NOMBRE_IP_PUBLICA}" \
    az network public-ip create -g "$GRUPO_RECURSOS" -n "$NOMBRE_IP_PUBLICA" -l "$REGION" \
    --sku Standard --allocation-method Static
}

asegurar_nic() {
  paso "Tarjeta de red: ${NOMBRE_NIC}"
  if recurso_existe network nic show -g "$GRUPO_RECURSOS" -n "$NOMBRE_NIC"; then
    registrar_existente "La tarjeta de red ${NOMBRE_NIC}"
    return 0
  fi
  ejecutar_cambio "la tarjeta de red ${NOMBRE_NIC}" \
    az network nic create -g "$GRUPO_RECURSOS" -n "$NOMBRE_NIC" -l "$REGION" \
    --vnet-name "$NOMBRE_RED_VIRTUAL" --subnet "$NOMBRE_SUBRED" \
    --network-security-group "$NOMBRE_GRUPO_SEGURIDAD" --public-ip-address "$NOMBRE_IP_PUBLICA"
}

# A "az vm create" NO se le pasa --vnet-name, --subnet, --public-ip-address
# ni --nsg: como la NIC ya se creó explícitamente en asegurar_nic, cualquiera
# de esas banderas hace que el CLI intente administrar la red él mismo, se
# invente nombres propios, y choque con lo que este script ya construyó.
asegurar_vm() {
  paso "Máquina virtual: ${NOMBRE_VM}"
  if recurso_existe vm show -g "$GRUPO_RECURSOS" -n "$NOMBRE_VM"; then
    registrar_existente "La VM ${NOMBRE_VM}"
    return 0
  fi

  ejecutar_cambio "la VM ${NOMBRE_VM}" \
    az vm create -g "$GRUPO_RECURSOS" -n "$NOMBRE_VM" --image "$IMAGEN_URN" \
    --nics "$NOMBRE_NIC" --size "$SKU_VM" --admin-username "$USUARIO_ADMIN" \
    --ssh-key-values "${LLAVES_SSH_ARRAY[@]}" --os-disk-size-gb "$TAMANO_DISCO_GB" \
    --storage-sku "$SKU_DISCO"
}

# Cada recurso se crea explícito y en este orden: nunca se deja que
# "az vm create" arme la red por su cuenta, porque se inventa nombres
# propios y rompe la premisa de "un solo grupo con nombres predecibles" que
# necesita el plan de mover los recursos a otra suscripción (ver
# propuesta-tablero-plataforma.md, "El plan de relevo cuando se acabe el
# crédito").
crear_recursos() {
  asegurar_grupo_recursos
  asegurar_red_virtual
  asegurar_nsg

  # Una IP pública de SKU Standard es deny por defecto en entrada, a
  # diferencia del SKU Basic retirado: sin estas tres reglas explícitas el
  # SSH no llega y parece que la VM está rota.
  asegurar_regla_nsg "permitir-ssh" 100 22 "$ORIGEN_SSH"
  asegurar_regla_nsg "permitir-http" 110 80 "*"
  asegurar_regla_nsg "permitir-https" 120 443 "*"

  asegurar_ip_publica
  asegurar_nic
  asegurar_vm
}

mostrar_resumen_cambios() {
  if [[ "${#RECURSOS_CREADOS[@]}" -gt 0 ]]; then
    if [[ "$SIMULAR" == "si" ]]; then
      info "Recursos que se crearían (${#RECURSOS_CREADOS[@]}): ${RECURSOS_CREADOS[*]}"
    else
      info "Recursos creados (${#RECURSOS_CREADOS[@]}): ${RECURSOS_CREADOS[*]}"
    fi
  fi

  if [[ "${#RECURSOS_EXISTENTES[@]}" -gt 0 ]]; then
    info "Recursos ya existentes (${#RECURSOS_EXISTENTES[@]}): ${RECURSOS_EXISTENTES[*]}"
  fi

  if [[ "$SIMULAR" == "si" ]]; then
    info "Cambios que se aplicarían: $(resumen_cambios)"
  else
    info "Cambios aplicados: $(resumen_cambios)"
  fi
}

# Imprime el comando de despliegue. Usa scp en lugar de rsync porque la imagen
# cloud de Debian de Azure (y también la caja de Vagrant) no trae rsync en el
# invitado: rsync necesita el binario en los dos extremos, así que si falta en
# el destino el comando falla. scp viaja sobre la conexión SSH que ya existe,
# sin necesitar paquetes extra.
mostrar_comando_siguiente() {
  local ip="$1"
  paso "Siguiente paso"
  cat <<EOF
  scp -r infra/servidor ${USUARIO_ADMIN}@${ip}:/tmp/infra-servidor
  ssh ${USUARIO_ADMIN}@${ip} 'sudo bash /tmp/infra-servidor/aprovisionar.sh'
EOF
}

# Relee el estado real en vez de confiar en lo que este script acaba de
# pedir: la IP pública nunca se escribe en ningún archivo, y el tamaño del
# disco se confirma contra lo que Azure reporta, no contra
# TAMANO_DISCO_GB (que solo agranda, nunca es garantía del tamaño final).
mostrar_cierre() {
  paso "Resumen"
  mostrar_resumen_cambios

  if [[ "$SIMULAR" == "si" ]]; then
    info "Modo --simular: no se tocó ningún recurso real. Ejecuta sin la bandera para crearlos."
    return 0
  fi

  local ip disco
  ip="$(az network public-ip show -g "$GRUPO_RECURSOS" -n "$NOMBRE_IP_PUBLICA" --query ipAddress -o tsv --only-show-errors)" \
    || error_fatal "No se pudo leer la IP pública de ${NOMBRE_IP_PUBLICA}."
  # Nota: el campo se llama diskSizeGB (GB en mayúscula). JMESPath distingue
  # mayúsculas, así que un error de capitalización devuelve cadena vacía en
  # silencio, sin fallar el query.
  disco="$(az vm show -d -g "$GRUPO_RECURSOS" -n "$NOMBRE_VM" --query storageProfile.osDisk.diskSizeGB -o tsv --only-show-errors)" \
    || error_fatal "No se pudo leer el tamaño del disco de ${NOMBRE_VM}."

  info "IP pública: ${ip}"
  info "Tamaño real del disco del sistema operativo: ${disco} GiB"

  mostrar_comando_siguiente "$ip"
}

principal() {
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --simular)
        SIMULAR="si"
        ;;
      --registrar-proveedores)
        REGISTRAR_PROVEEDORES="si"
        ;;
      --ayuda | -h)
        mostrar_ayuda
        return 0
        ;;
      *)
        error_fatal "Opción desconocida: $1. Usa --ayuda para ver las opciones disponibles."
        ;;
    esac
    shift
  done

  verificar_preflight
  crear_recursos
  mostrar_cierre
}

principal "$@"
