#!/usr/bin/env bash
# infra/azure/crear-servidor.sh
# Crea en Azure los recursos del servidor de Centinela: grupo de recursos, red
# virtual, NSG con sus reglas, IP pública, NIC y VM, en ese orden (SCRUM-26).
set -euo pipefail

DIRECTORIO_SCRIPT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Usa su propio lib/comun.sh, no el de infra/servidor/: esos helpers asumen
# que corren como root en el servidor.
# shellcheck source=lib/comun.sh
source "${DIRECTORIO_SCRIPT}/lib/comun.sh"

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
                             al puerto SSH (el número lo fija PUERTO_SSH en
                             infra/servidor/red-fijada.sh). Vale "*" por
                             defecto: abierto a todo internet a propósito
                             (ver el comentario de ORIGEN_SSH en
                             infra/azure/variables.sh sobre por qué, y
                             sobre cuál es la protección real). Si se
                             define con otro valor, tiene que ser una IPv4
                             o un CIDR.

Requiere "az login" vigente, incluso en modo --simular: la simulación lee
el estado real de la suscripción para ser fiel a lo que pasaría.

No incluye ningún camino de rollback ni de teardown: borrar recursos en la
nube necesita que lo decida un humano.
EOF
}

# confirmar_suscripcion lee SIMULAR por nombre de variable: basta con que
# esté definida antes de llamarla.

# obligatorio=si corta con error_fatal si el proveedor no está Registered
# (o lo registra con --registrar-proveedores); en no, solo avisa.
verificar_proveedor() {
  local proveedor="$1" obligatorio="$2" estado

  estado="$(az provider show -n "$proveedor" --query registrationState -o tsv --only-show-errors 2>/dev/null || true)"

  if [[ "$estado" == "Registered" ]]; then
    info "Proveedor ${proveedor} ya está registrado."
    return 0
  fi

  if [[ "$obligatorio" == "si" && "$REGISTRAR_PROVEEDORES" == "si" ]]; then
    # --wait: el registro es asíncrono; sin esperar, el resto del script
    # seguiría sin el proveedor Registered.
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

# Valida que cada elemento sea IPv4 o CIDR. El chequeo de vacío va acá
# adentro: una cadena vacía no dispara el bucle y pasaría en silencio.
validar_formato_origen_ssh() {
  local origen="$1"
  local elemento

  if [[ -z "${origen// }" ]]; then
    error_fatal "ORIGEN_SSH quedó vacía (y no vale \"*\"). Verifica que 'curl -fsS https://ifconfig.me' devolvió una IP y no una respuesta vacía por un fallo de red o un portal cautivo. Si de verdad quieres el origen abierto, usa ORIGEN_SSH='*' explícitamente."
  fi

  read -ra elementos <<<"$origen"
  for elemento in "${elementos[@]}"; do
    if ! [[ $elemento =~ ^[0-9]{1,3}(\.[0-9]{1,3}){3}(/[0-9]{1,2})?$ ]]; then
      local truncado="${elemento:0:60}"
      error_fatal "ORIGEN_SSH contiene un elemento inválido: '${truncado}'. Se esperaba una IPv4 o un CIDR (ej: 190.67.159.249 o 190.67.0.0/16). Verifica que 'curl -fsS https://ifconfig.me' devolvió una IP, no una página HTML de error o una respuesta de un portal cautivo."
    fi
  done
}

# Con ORIGEN_SSH="*" no hay nada que validar; con un valor explícito, sigue
# exigiendo IPv4 o CIDR (validar_formato_origen_ssh).
verificar_origen_ssh() {
  if [[ "$ORIGEN_SSH" == "*" ]]; then
    info "ORIGEN_SSH=* (por defecto): el puerto ${PUERTO_SSH} queda abierto a todo internet. La protección real contra acceso no autorizado es la autenticación exclusivamente por llave que fija infra/servidor/pasos/10-ssh.sh, no el origen de esta regla."
    return 0
  fi

  validar_formato_origen_ssh "$ORIGEN_SSH"
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

# Usa el rango de direcciones por defecto del CLI (10.0.0.0/16 red,
# 10.0.0.0/24 subred): no se declara explícito.
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

# Imprime origen, puerto y prioridad de la regla existente: aceptar en
# silencio una regla SSH con el origen equivocado es una falla de seguridad.
mostrar_aviso_regla_existente() {
  local nombre="$1" salida
  local -a campos
  local origen_actual puerto_actual prioridad_actual

  salida="$(az network nsg rule show --nsg-name "$NOMBRE_GRUPO_SEGURIDAD" -g "$GRUPO_RECURSOS" -n "$nombre" \
    --query "[sourceAddressPrefix,destinationPortRange,priority]" -o tsv --only-show-errors)" \
    || error_fatal "No se pudo leer el detalle de la regla NSG ${nombre}."
  # az -o tsv emite una línea por campo, no tabulados en una sola línea: por
  # eso mapfile, no read.
  mapfile -t campos <<<"$salida"
  origen_actual="${campos[0]}"
  puerto_actual="${campos[1]}"
  prioridad_actual="${campos[2]}"

  aviso "La regla NSG ${nombre} ya existe con origen=${origen_actual}, puerto=${puerto_actual}, prioridad=${prioridad_actual}. No se edita en su lugar: una regla puede existir con el mismo nombre pero otro puerto, otro origen u otra prioridad, y eso lo tiene que revisar una persona."
}

# Nunca se edita una regla existente en su lugar: si cambia origen o
# prioridad, se borra y se recrea a mano, no desde este script.
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

# NO se pasan --vnet-name/--subnet/--public-ip-address/--nsg: con la NIC ya
# creada, esas banderas hacen que el CLI arme su propia red y choque con esto.
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

# Orden fijo y explícito: por qué no se deja que "az vm create" arme la red
# sola está en asegurar_vm.
crear_recursos() {
  asegurar_grupo_recursos
  asegurar_red_virtual
  asegurar_nsg

  # IP Standard es deny por defecto en entrada: sin estas 3 reglas no hay SSH.
  # La regla nueva coexiste con la vieja durante la transición (mostrar_cierre).
  # Prioridades con huecos de 5/10: 100 vieja, 105 nueva, 110 http, 120 https.
  asegurar_regla_nsg "permitir-ssh-${PUERTO_SSH}" 105 "$PUERTO_SSH" "$ORIGEN_SSH"
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

# Ver infra/servidor/README.md para el porqué de scp y del destino en /opt.
# -p (ssh) vs -P mayúscula (scp) es inconsistencia real entre los binarios.
# Se imprimen dos variantes porque, mientras exista la regla NSG vieja del
# puerto 22, no se sabe desde acá si esta VM ya pasó por SCRUM-28.
mostrar_comando_siguiente() {
  local ip="$1"
  local regla_vieja_existe="$2"
  local destino_remoto="/tmp/infra-servidor"
  local instalar_remoto="sudo mkdir -p /opt/centinela && sudo cp -rT ${destino_remoto} /opt/centinela/infra-servidor && sudo bash /opt/centinela/infra-servidor/aprovisionar.sh"

  paso "Siguiente paso"

  if [[ "$regla_vieja_existe" == "si" ]]; then
    cat <<EOF
  Todavía existe la regla NSG del puerto 22 ("permitir-ssh"), así que no se
  sabe desde acá si esta VM ya pasó por el endurecimiento de SSH (SCRUM-28)
  o si es la primera vez. Dos variantes:

  Primera vez (VM recién creada, sshd todavía escucha en el puerto 22):
    scp -r infra/servidor ${USUARIO_ADMIN}@${ip}:${destino_remoto}
    ssh ${USUARIO_ADMIN}@${ip} '${instalar_remoto}'

  Ya aprovisionada antes (sshd ya está en el puerto ${PUERTO_SSH}):
    scp -P ${PUERTO_SSH} -r infra/servidor ${USUARIO_ADMIN}@${ip}:${destino_remoto}
    ssh -p ${PUERTO_SSH} ${USUARIO_ADMIN}@${ip} '${instalar_remoto}'
EOF
  else
    cat <<EOF
  scp -P ${PUERTO_SSH} -r infra/servidor ${USUARIO_ADMIN}@${ip}:${destino_remoto}
  ssh -p ${PUERTO_SSH} ${USUARIO_ADMIN}@${ip} '${instalar_remoto}'
EOF
  fi
}

# Relee el estado real: la IP nunca se guarda en archivo, y el disco se
# confirma contra lo que Azure reporta (TAMANO_DISCO_GB solo agranda, no
# garantiza el tamaño final).
mostrar_cierre() {
  paso "Resumen"
  mostrar_resumen_cambios

  # El aviso de la regla vieja desaparece solo cuando se borra la regla, sin
  # que nadie tenga que editar el script. recurso_existe es una lectura, así
  # que corre también en --simular.
  local regla_vieja_existe="no"
  if recurso_existe network nsg rule show --nsg-name "$NOMBRE_GRUPO_SEGURIDAD" -g "$GRUPO_RECURSOS" -n "permitir-ssh"; then
    regla_vieja_existe="si"
    aviso "La regla NSG vieja 'permitir-ssh' (puerto 22) todavía existe. Verifica primero, desde OTRA terminal, que la conexión funciona por el puerto ${PUERTO_SSH} (ssh -p ${PUERTO_SSH} ${USUARIO_ADMIN}@<ip>). Solo después de confirmarlo, bórrala con: az network nsg rule delete --nsg-name ${NOMBRE_GRUPO_SEGURIDAD} -g ${GRUPO_RECURSOS} -n permitir-ssh. Ese borrado lo hace una persona, nunca este script: la política es que una regla existente no se edita ni se borra desde código."
  fi

  if [[ "$SIMULAR" == "si" ]]; then
    info "Modo --simular: no se tocó ningún recurso real. Ejecuta sin la bandera para crearlos."
    return 0
  fi

  local ip disco
  ip="$(az network public-ip show -g "$GRUPO_RECURSOS" -n "$NOMBRE_IP_PUBLICA" --query ipAddress -o tsv --only-show-errors)" \
    || error_fatal "No se pudo leer la IP pública de ${NOMBRE_IP_PUBLICA}."
  # Campo diskSizeGB (GB mayúscula): JMESPath distingue mayúsculas, un typo
  # devuelve cadena vacía en silencio.
  disco="$(az vm show -d -g "$GRUPO_RECURSOS" -n "$NOMBRE_VM" --query storageProfile.osDisk.diskSizeGB -o tsv --only-show-errors)" \
    || error_fatal "No se pudo leer el tamaño del disco de ${NOMBRE_VM}."

  info "IP pública: ${ip}"
  info "Tamaño real del disco del sistema operativo: ${disco} GiB"

  mostrar_comando_siguiente "$ip" "$regla_vieja_existe"
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
