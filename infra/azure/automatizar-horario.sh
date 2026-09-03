#!/usr/bin/env bash
# infra/azure/automatizar-horario.sh
#
# Tarjeta SCRUM-29 ("Automatizar el encendido y el apagado diario de la
# máquina"). Crea en Azure una cuenta de Automation con identidad
# administrada, dos runbooks de PowerShell (encender/apagar centinela-vm) y
# dos horarios diarios vinculados a ellos. Reemplaza TANTO el auto-shutdown
# nativo de Azure como una GitHub Action programada: un solo mecanismo
# controla los dos horarios (encendido y apagado), en vez de dos sistemas
# distintos.
#
# Horario: todos los días, 08:00 a 12:00 hora Colombia (COT, UTC-5,
# Colombia nunca tiene horario de verano). Encender a las 08:00 COT (13:00
# UTC), apagar (deallocate, no shutdown del sistema operativo) a las 12:00
# COT (17:00 UTC).
#
# Por qué Automation Account + identidad administrada en vez de una GitHub
# Action con "az vm start" (el diseño original de la tarjeta): el tenant de
# Entra ID de este proyecto (UCompensar) tiene
# allowedToCreateApps=false (verificado con
# az rest --method get --url "https://graph.microsoft.com/v1.0/policies/authorizationPolicy"
# --query "defaultUserRolePermissions.allowedToCreateApps" -o tsv) y el
# usuario no tiene ningún rol de directorio elevado (verificado con
# az rest --method get --url "https://graph.microsoft.com/v1.0/me/transitiveMemberOf/microsoft.graph.directoryRole"
# --query "value[].displayName" -o tsv, vacío). Sin poder crear un App
# Registration, ni OIDC federado ni "az ad sp create-for-rbac" son viables:
# los dos necesitan crear un App Registration. Una identidad administrada de
# un Automation Account no tiene esa restricción: la crea el propio recurso
# de Azure, no Entra ID.
#
# Por qué PowerShell y no Python para los runbooks: Microsoft recomienda
# PowerShell 7.4 LTS para runbooks nuevos, y no hay precedente de Python en
# este repositorio.
#
# Por qué dos runbooks propios y no la solución empaquetada "Start/Stop VMs
# v2" de Microsoft: esa solución agrega Function App, Storage Account, Logic
# App y Application Insights, pensada para flotas de VMs en varias
# suscripciones. Para una sola VM es sobre-ingeniería.
#
# Corre en el portátil del operador, igual que infra/azure/crear-servidor.sh
# (ver la cabecera de ese archivo): nunca en el servidor real.
#
# Banderas: --simular y --ayuda. A diferencia de crear-servidor.sh, este
# script NO tiene --registrar-proveedores: la tarjeta no lo pidió. El
# proveedor Microsoft.Automation aparece HOY como NotRegistered en esta
# suscripción (verificado con az provider show -n Microsoft.Automation
# --query registrationState -o tsv), a diferencia de Microsoft.Compute y
# Microsoft.Network, que crear-servidor.sh ya dejó Registered. Azure suele
# registrar un proveedor de forma implícita en la primera escritura real
# contra él, así que "az automation account create" puede resolverlo solo;
# si no lo hace, va a fallar con un mensaje explícito pidiendo el registro,
# y ahí un humano decide si lo registra (az provider register -n
# Microsoft.Automation --wait), igual que con Microsoft.Storage en
# crear-servidor.sh: es un cambio a nivel de suscripción que comparte el
# equipo, no algo para automatizar en silencio.
#
# Este script NO incluye ningún camino de rollback ni de teardown, por la
# misma razón que crear-servidor.sh: borrar recursos en la nube necesita que
# lo decida un humano.
set -euo pipefail

DIRECTORIO_SCRIPT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# shellcheck source=lib/comun.sh
source "${DIRECTORIO_SCRIPT}/lib/comun.sh"

# shellcheck source=variables.sh
source "${DIRECTORIO_SCRIPT}/variables.sh"

SIMULAR="no"

# principalId de la identidad administrada del Automation Account. La llena
# asegurar_identidad_administrada y la leen asegurar_rol_vm y (indirectamente,
# a través del Automation Account ya identificado por nombre) los pasos
# siguientes. Queda vacía si --simular corre por primera vez, antes de que
# exista una cuenta real (ver el comentario dentro de esa función).
PRINCIPAL_ID=""

mostrar_ayuda() {
  cat <<'EOF'
Uso: automatizar-horario.sh [opciones]

Crea en Azure la automatización de encendido/apagado diario de la VM de
Centinela (tarjeta SCRUM-29): cuenta de Automation, identidad administrada,
el rol "Virtual Machine Contributor" sobre la VM, dos runbooks de
PowerShell y dos horarios diarios vinculados a ellos. Lee su configuración
de infra/azure/variables.sh.

Opciones:
  --simular   Imprime lo que haría, sin ejecutar ningún "az" que escriba.
              Las lecturas sí corren, para que la simulación refleje el
              estado real de la suscripción. Igual que en
              infra/azure/crear-servidor.sh, en una corrida --simular contra
              una suscripción donde todavía no existe la cuenta de
              Automation, los pasos que dependen de leer su id o su
              principalId no tienen un valor real que leer: en vez de
              fallar, avisan que ese dato solo se conoce en una corrida
              real y siguen adelante.
  --ayuda     Muestra esta ayuda.

Sin opciones, corre la verificación previa y crea los recursos que falten.

Variables de entorno relevantes:
  CONFIRMAR_SUSCRIPCION=si  Salta la confirmación interactiva de la
                             suscripción activa. Úsala solo si ya la
                             verificaste por otro medio.

Requiere "az login" vigente, incluso en modo --simular.

No incluye ningún camino de rollback ni de teardown: borrar recursos en la
nube necesita que lo decida un humano.
EOF
}

verificar_preflight() {
  paso "Verificación previa"
  verificar_cli_az
  confirmar_suscripcion
}

asegurar_automation_account() {
  paso "Cuenta de automatización: ${NOMBRE_AUTOMATION}"
  if recurso_existe automation account show -g "$GRUPO_RECURSOS" -n "$NOMBRE_AUTOMATION"; then
    registrar_existente "La cuenta de automatización ${NOMBRE_AUTOMATION}"
    return 0
  fi
  ejecutar_cambio "la cuenta de automatización ${NOMBRE_AUTOMATION}" \
    az automation account create -g "$GRUPO_RECURSOS" -n "$NOMBRE_AUTOMATION" -l "$REGION" --sku Basic
}

# La extensión "automation" del CLI (1.0.0b2, la última disponible) no tiene
# un subcomando "identity" dedicado (no existe
# "az automation account identity assign"). "az resource update --set
# identity.type=SystemAssigned" es el intento principal; el fallback
# documentado si ese comando fallara es
# "az rest --method patch --url https://management.azure.com<id>?api-version=2024-10-23
# --body '{"identity":{"type":"SystemAssigned"}}'". NINGUNO de los dos se
# pudo ejercitar de verdad en esta implementación: crear la identidad exige
# que la cuenta de Automation ya exista de verdad, y --simular nunca la
# crea (ver mostrar_ayuda). Cuál de los dos funciona queda por confirmar en
# la primera corrida real, sin --simular.
asegurar_identidad_administrada() {
  paso "Identidad administrada de ${NOMBRE_AUTOMATION}"

  local id_cuenta tipo_actual

  if recurso_existe automation account show -g "$GRUPO_RECURSOS" -n "$NOMBRE_AUTOMATION"; then
    id_cuenta="$(az automation account show -g "$GRUPO_RECURSOS" -n "$NOMBRE_AUTOMATION" --query id -o tsv --only-show-errors)" \
      || error_fatal "No se pudo leer el id de la cuenta de automatización ${NOMBRE_AUTOMATION}."
    # No se trata como error fatal: si la cuenta nunca tuvo identidad
    # asignada, este query puede fallar o devolver una cadena vacía en vez
    # de "None".
    tipo_actual="$(az automation account show -g "$GRUPO_RECURSOS" -n "$NOMBRE_AUTOMATION" --query identity.type -o tsv --only-show-errors 2>/dev/null || true)"
  else
    # Solo pasa bajo --simular, cuando el paso anterior no creó la cuenta de
    # verdad. El id de un Automation Account es predecible (no lo asigna
    # Azure al azar), así que igual se puede mostrar en la simulación.
    id_cuenta="/subscriptions/$(az account show --query id -o tsv --only-show-errors)/resourceGroups/${GRUPO_RECURSOS}/providers/Microsoft.Automation/automationAccounts/${NOMBRE_AUTOMATION}"
    tipo_actual=""
  fi

  if [[ "$tipo_actual" == "SystemAssigned" ]]; then
    registrar_existente "La identidad administrada de ${NOMBRE_AUTOMATION} (SystemAssigned)"
  else
    ejecutar_cambio "la identidad administrada de ${NOMBRE_AUTOMATION}" \
      az resource update --ids "$id_cuenta" --set identity.type=SystemAssigned
  fi

  if [[ "$SIMULAR" == "si" && "$tipo_actual" != "SystemAssigned" ]]; then
    info "[simular] La identidad no se asignó de verdad: no hay un principalId real que leer todavía. Una corrida real sí lo tendría antes de continuar con el rol sobre la VM."
    PRINCIPAL_ID=""
    return 0
  fi

  # La propagación de una identidad recién creada a Microsoft Graph/RBAC
  # puede tardar unos segundos: sin este reintento acotado, la primera
  # lectura del principalId justo después de crear la identidad suele
  # devolver vacío. No se generaliza a un helper de reintento en lib/comun.sh
  # porque, hoy, solo esta función lo necesita.
  local intento
  for intento in $(seq 1 9); do
    PRINCIPAL_ID="$(az automation account show -g "$GRUPO_RECURSOS" -n "$NOMBRE_AUTOMATION" --query identity.principalId -o tsv --only-show-errors 2>/dev/null || true)"
    if [[ -n "$PRINCIPAL_ID" ]]; then
      break
    fi
    info "Esperando a que se propague el principalId de la identidad administrada (intento ${intento}/9)..."
    sleep 10
  done

  [[ -n "$PRINCIPAL_ID" ]] \
    || error_fatal "La identidad administrada de ${NOMBRE_AUTOMATION} no propagó un principalId después de 90s."

  info "principalId: ${PRINCIPAL_ID}"
}

# --assignee-object-id + --assignee-principal-type ServicePrincipal en vez
# de --assignee <principalId> a secas: evita que el CLI intente resolver el
# principal contra Microsoft Graph por otro campo distinto al id, que puede
# fallar por la misma demora de propagación de asegurar_identidad_administrada.
asegurar_rol_vm() {
  paso "Rol 'Virtual Machine Contributor' sobre ${NOMBRE_VM}"

  local id_vm
  id_vm="$(az vm show -g "$GRUPO_RECURSOS" -n "$NOMBRE_VM" --query id -o tsv --only-show-errors)" \
    || error_fatal "No se pudo leer el id de la VM ${NOMBRE_VM}."

  if [[ -z "$PRINCIPAL_ID" ]]; then
    # Solo pasa bajo --simular en una cuenta de Automation que todavía no
    # existe de verdad (ver asegurar_identidad_administrada): sin un
    # principalId real no hay nada que consultar ni que asignar.
    info "[simular] Sin un principalId real todavía no se puede simular este paso. En una corrida real, asignaría 'Virtual Machine Contributor' sobre ${id_vm} al principal de la identidad administrada."
    return 0
  fi

  local asignacion_existente
  asignacion_existente="$(az role assignment list --assignee-object-id "$PRINCIPAL_ID" --assignee-principal-type ServicePrincipal \
    --scope "$id_vm" --role "Virtual Machine Contributor" --query "[0].id" -o tsv --only-show-errors 2>/dev/null || true)"

  if [[ -n "$asignacion_existente" ]]; then
    registrar_existente "El rol 'Virtual Machine Contributor' de ${NOMBRE_AUTOMATION} sobre ${NOMBRE_VM}"
    return 0
  fi

  ejecutar_cambio "el rol 'Virtual Machine Contributor' de ${NOMBRE_AUTOMATION} sobre ${NOMBRE_VM}" \
    az role assignment create --assignee-object-id "$PRINCIPAL_ID" --assignee-principal-type ServicePrincipal \
    --role "Virtual Machine Contributor" --scope "$id_vm"
}

# Los dos runbooks (infra/azure/runbooks/encender-vm.ps1 y apagar-vm.ps1) se
# crean SIN parámetros, ni de PowerShell ni de job-schedule: decisión
# explícita para eliminar de raíz el riesgo de casing de parámetros al
# vincular horario+runbook con "az rest" (ver asegurar_vinculo_horario más
# abajo, cuyo cuerpo JSON no lleva bloque "parameters" por esto mismo). El
# nombre de la VM y el grupo de recursos quedan escritos literalmente dentro
# de cada .ps1: este proyecto solo tiene una VM (centinela-vm en
# centinela-rg), así que parametrizarlo sería una abstracción que nadie va a
# usar.
#
# Pendiente de confirmar en la primera corrida real (nota para la bitácora,
# no algo que este script resuelva): la versión del runtime de PowerShell
# del runbook (5.1 legado vs 7.4 con Az). La extensión "automation" del CLI
# no tiene "--runtime-environment" en "az automation runbook create --help"
# (verificado), así que solo se puede confirmar corriendo el runbook de
# verdad y revisando "az automation runtime-environment list" contra la
# cuenta ya creada.
#
# Chequeo de idempotencia por EXISTENCIA (¿hay un runbook con este nombre?)
# Y por CONTENIDO (¿el contenido publicado es igual al archivo local?),
# siguiendo el mismo espíritu que asegurar_archivo_con_contenido y
# asegurar_servicio_compose de infra/servidor/lib/comun.sh: medir el cambio
# real, no solo si el recurso ya existe.
#
# "az automation runbook show" no devuelve el contenido publicado: hay que
# leerlo con "az rest" contra el endpoint "/content", que responde texto
# plano, no JSON.
asegurar_runbook() {
  local nombre="$1" archivo_local="$2" tipo="${3:-PowerShell}"
  local contenido_local contenido_remoto id_runbook

  paso "Runbook: ${nombre}"

  contenido_local="$(cat "$archivo_local")"

  if recurso_existe automation runbook show -g "$GRUPO_RECURSOS" --automation-account-name "$NOMBRE_AUTOMATION" -n "$nombre"; then
    registrar_existente "El runbook ${nombre}"
    id_runbook="$(az automation runbook show -g "$GRUPO_RECURSOS" --automation-account-name "$NOMBRE_AUTOMATION" -n "$nombre" --query id -o tsv --only-show-errors)" \
      || error_fatal "No se pudo leer el id del runbook ${nombre}."
    contenido_remoto="$(az rest --method get --url "https://management.azure.com${id_runbook}/content?api-version=2024-10-23" --only-show-errors 2>/dev/null || true)"
  else
    # Bajo --simular contra una cuenta de Automation que todavía no existe
    # de verdad, este runbook tampoco existe: no hay contenido remoto que
    # leer, así que se trata como distinto a propósito, para que el resumen
    # muestre que también se publicaría el contenido, no solo que se
    # crearía el runbook vacío.
    ejecutar_cambio "el runbook ${nombre} (creación)" \
      az automation runbook create -g "$GRUPO_RECURSOS" --automation-account-name "$NOMBRE_AUTOMATION" -n "$nombre" --type "$tipo" -l "$REGION"
    contenido_remoto=""
  fi

  if [[ "$contenido_remoto" == "$contenido_local" ]]; then
    info "El contenido publicado de ${nombre} ya coincide con ${archivo_local}."
    return 0
  fi

  ejecutar_cambio "el contenido del runbook ${nombre}" \
    az automation runbook replace-content --automation-account-name "$NOMBRE_AUTOMATION" -g "$GRUPO_RECURSOS" \
    -n "$nombre" --content "@${archivo_local}"

  ejecutar_cambio "la publicación del runbook ${nombre}" \
    az automation runbook publish --automation-account-name "$NOMBRE_AUTOMATION" -g "$GRUPO_RECURSOS" -n "$nombre"
}

# Devuelve (por stdout) el epoch Unix de la próxima ocurrencia de "hora_utc"
# (formato "HH:MM"): hoy si esa hora todavía no pasó, mañana si ya pasó.
# Azure Automation rechaza un start-time en el pasado, así que el horario no
# puede quedar fijo: cada corrida de este script tiene que recalcularlo,
# para que sea re-ejecutable sin fallar por una fecha vieja.
calcular_proxima_ocurrencia() {
  local hora_utc="$1"
  local fecha_hoy epoch_objetivo epoch_ahora

  fecha_hoy="$(date -u +%Y-%m-%d)"
  epoch_objetivo="$(date -u -d "${fecha_hoy}T${hora_utc}:00Z" +%s)"
  epoch_ahora="$(date -u +%s)"

  if [[ "$epoch_objetivo" -le "$epoch_ahora" ]]; then
    epoch_objetivo=$((epoch_objetivo + 86400))
  fi

  printf '%s' "$epoch_objetivo"
}

# Imprime los valores actuales del horario existente (frecuencia, zona,
# inicio) para que un humano pueda comparar contra lo que se esperaba.
# Mismo espíritu que mostrar_aviso_regla_existente de crear-servidor.sh: un
# horario existente NUNCA se edita en su lugar.
mostrar_aviso_horario_existente() {
  local nombre="$1" salida
  local -a campos
  local frecuencia_actual zona_actual inicio_actual

  salida="$(az automation schedule show --automation-account-name "$NOMBRE_AUTOMATION" -g "$GRUPO_RECURSOS" -n "$nombre" \
    --query "[frequency,timeZone,startTime]" -o tsv --only-show-errors)" \
    || error_fatal "No se pudo leer el detalle del horario ${nombre}."
  mapfile -t campos <<<"$salida"
  frecuencia_actual="${campos[0]}"
  zona_actual="${campos[1]}"
  inicio_actual="${campos[2]}"

  aviso "El horario ${nombre} ya existe con frecuencia=${frecuencia_actual}, zona=${zona_actual}, inicio=${inicio_actual}. No se edita en su lugar: si el equipo necesita otro horario, se borra y se recrea a mano, a propósito, no desde este script."
}

# Prueba primero --time-zone "America/Bogota" (más legible en el Portal que
# un horario en UTC). Si Azure lo rechaza, cae a --time-zone "UTC" con el
# horario ya convertido (13:00 y 17:00 UTC): Colombia nunca tiene horario de
# verano, así que la conversión es fija y no cambia nada en la práctica. Los
# dos intentos usan el mismo instante real (el mismo epoch), solo cambia
# cómo se escribe ese instante para cada zona horaria.
#
# El offset (%:z: "-05:00" o "+00:00") va SIEMPRE embebido en --start-time,
# nunca se manda una hora "desnuda" como "2026-09-03T08:00:00": el campo
# startTime de la API es un date-time, y una hora sin offset se puede leer
# como UTC del lado del servidor sin que este script se entere. Sin el
# offset, un "08:00" pensado como hora de Bogotá podría terminar
# ejecutándose a las 08:00 UTC (3:00 a.m. en Colombia) en silencio: el
# horario quedaría mal sin que ningún chequeo de este script lo detectara.
# Con el offset embebido, los dos intentos son instantes absolutos e
# inequívocos sin importar cómo Azure interprete --time-zone.
asegurar_horario() {
  local nombre="$1" hora_utc="$2" descripcion="$3"

  paso "Horario: ${nombre} (${hora_utc} UTC)"

  if recurso_existe automation schedule show --automation-account-name "$NOMBRE_AUTOMATION" -g "$GRUPO_RECURSOS" -n "$nombre"; then
    registrar_existente "El horario ${nombre}"
    mostrar_aviso_horario_existente "$nombre"
    return 0
  fi

  local epoch_proximo inicio_bogota inicio_utc
  epoch_proximo="$(calcular_proxima_ocurrencia "$hora_utc")"
  inicio_bogota="$(TZ="America/Bogota" date -d "@${epoch_proximo}" +%Y-%m-%dT%H:%M:%S%:z)"
  inicio_utc="$(date -u -d "@${epoch_proximo}" +%Y-%m-%dT%H:%M:%S%:z)"

  if [[ "$SIMULAR" == "si" ]]; then
    info "[simular] Se ejecutaría: az automation schedule create --automation-account-name ${NOMBRE_AUTOMATION} -g ${GRUPO_RECURSOS} -n ${nombre} --frequency Day --interval 1 --start-time ${inicio_bogota} --time-zone America/Bogota --description '${descripcion}' (con fallback a --start-time ${inicio_utc} --time-zone UTC si America/Bogota fuera rechazado)."
    registrar_cambio
    RECURSOS_CREADOS+=("el horario ${nombre}")
    return 0
  fi

  # Se captura el stderr del primer intento para poder citarlo en el aviso
  # si hay que caer al fallback: así el log dice lo que Azure respondió de
  # verdad, no solo que "lo rechazó" sin evidencia.
  local error_intento_1
  if error_intento_1="$(az automation schedule create --automation-account-name "$NOMBRE_AUTOMATION" -g "$GRUPO_RECURSOS" \
      -n "$nombre" --frequency Day --interval 1 --start-time "$inicio_bogota" \
      --time-zone "America/Bogota" --description "$descripcion" -o none --only-show-errors 2>&1)"; then
    info "Horario ${nombre} creado con --time-zone America/Bogota."
  elif az automation schedule create --automation-account-name "$NOMBRE_AUTOMATION" -g "$GRUPO_RECURSOS" \
      -n "$nombre" --frequency Day --interval 1 --start-time "$inicio_utc" \
      --time-zone "UTC" --description "$descripcion" -o none --only-show-errors; then
    aviso "El intento con --time-zone America/Bogota para ${nombre} falló; se creó con --time-zone UTC en su lugar (mismo instante real, Colombia no tiene horario de verano). Respuesta del primer intento: ${error_intento_1}"
  else
    error_fatal "No se pudo crear el horario ${nombre} ni con --time-zone America/Bogota ni con UTC. Respuesta del primer intento: ${error_intento_1}"
  fi

  registrar_cambio
  RECURSOS_CREADOS+=("el horario ${nombre}")
}

# La extensión "automation" del CLI no tiene grupo "job-schedule" (solo
# existen account/configuration/hrwg/job/python3-package/runbook/
# runtime-environment/schedule/software-update-configuration/source-control),
# así que el vínculo horario-runbook se hace con "az rest" directo contra la
# API de ARM.
asegurar_vinculo_horario() {
  local nombre_horario="$1" nombre_runbook="$2"
  local suscripcion url_base coincidencias

  paso "Vínculo horario-runbook: ${nombre_horario} -> ${nombre_runbook}"

  suscripcion="$(az account show --query id -o tsv --only-show-errors)" \
    || error_fatal "No se pudo leer el id de la suscripción activa."
  url_base="https://management.azure.com/subscriptions/${suscripcion}/resourceGroups/${GRUPO_RECURSOS}/providers/Microsoft.Automation/automationAccounts/${NOMBRE_AUTOMATION}"

  # Vacío (o "[]") tanto si de verdad no hay vínculo como si la cuenta de
  # Automation todavía no existe (--simular contra infraestructura nueva):
  # en los dos casos, lo correcto es seguir como si no existiera todavía.
  coincidencias="$(az rest --method get --url "${url_base}/jobSchedules?api-version=2024-10-23" \
    --query "value[?properties.schedule.name=='${nombre_horario}' && properties.runbook.name=='${nombre_runbook}']" \
    -o json --only-show-errors 2>/dev/null || true)"

  if [[ -n "$coincidencias" && "$coincidencias" != "[]" ]]; then
    registrar_existente "El vínculo ${nombre_horario} -> ${nombre_runbook}"
    return 0
  fi

  local guid cuerpo
  guid="$(uuidgen)"
  # Sin bloque "parameters": los runbooks de este script no toman
  # parámetros a propósito (ver el comentario sobre asegurar_runbook más
  # arriba), así que no hay nada que pasarles al vincularlos.
  cuerpo="$(printf '{"properties":{"schedule":{"name":"%s"},"runbook":{"name":"%s"}}}' "$nombre_horario" "$nombre_runbook")"

  ejecutar_cambio "el vínculo ${nombre_horario} -> ${nombre_runbook}" \
    az rest --method put --url "${url_base}/jobSchedules/${guid}?api-version=2024-10-23" --body "$cuerpo"
}

crear_recursos() {
  asegurar_automation_account
  asegurar_identidad_administrada
  asegurar_rol_vm

  asegurar_runbook "encender-vm" "${DIRECTORIO_SCRIPT}/runbooks/encender-vm.ps1"
  asegurar_runbook "apagar-vm" "${DIRECTORIO_SCRIPT}/runbooks/apagar-vm.ps1"

  # 13:00 UTC = 08:00 COT (encendido) · 17:00 UTC = 12:00 COT (apagado).
  asegurar_horario "encender-vm-diario" "13:00" "Enciende ${NOMBRE_VM} todos los días a las 08:00 COT (SCRUM-29)."
  asegurar_horario "apagar-vm-diario" "17:00" "Apaga (deallocate) ${NOMBRE_VM} todos los días a las 12:00 COT (SCRUM-29)."

  asegurar_vinculo_horario "encender-vm-diario" "encender-vm"
  asegurar_vinculo_horario "apagar-vm-diario" "apagar-vm"
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

mostrar_cierre() {
  paso "Resumen"
  mostrar_resumen_cambios

  if [[ "$SIMULAR" == "si" ]]; then
    info "Modo --simular: no se tocó ningún recurso real. Ejecuta sin la bandera para crearlos."
    return 0
  fi
}

principal() {
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --simular)
        SIMULAR="si"
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
