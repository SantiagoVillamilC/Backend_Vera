#!/usr/bin/env bash
# infra/azure/automatizar-horario.sh
#
# Tarjeta SCRUM-29 ("Automatizar el encendido y el apagado diario de la
# máquina"). Crea en Azure dos Logic Apps (plan Consumption), cada uno con
# identidad administrada system-assigned, un trigger de Recurrence diario y
# una acción HTTP contra la API de Azure Resource Manager para
# encender/apagar (deallocate) centinela-vm. Reemplaza TANTO el
# auto-shutdown nativo de Azure como una GitHub Action programada: un solo
# mecanismo controla los dos horarios (encendido y apagado), en vez de dos
# sistemas distintos.
#
# Horario: todos los días, 08:00 a 12:00 hora Colombia (COT, UTC-5,
# Colombia nunca tiene horario de verano). Encender a las 08:00 COT (13:00
# UTC), apagar (deallocate, no shutdown del sistema operativo) a las 12:00
# COT (17:00 UTC).
#
# Por qué Logic Apps y no Automation Account (el diseño original de esta
# tarjeta, todavía visible en el historial de git de este archivo):
# Automation Account para suscripciones "Azure for Students" solo se puede
# crear en un conjunto de regiones (eastus, eastus2, westus, northeurope,
# southeastasia, japanwest) que no tiene ninguna intersección con las
# regiones que permite la política de esta suscripción (chilecentral,
# northcentralus, southcentralus, brazilsouth, centralus). Bloqueo de
# plataforma sin solución, verificado en vivo el 3 de septiembre de 2026 al
# intentar crear la cuenta contra Azure real. Detalle completo en
# proyecto-fin-de-grado/bitacora-tecnica/2026-09-03-automation-account-choque-region.md
# (repo de tesis, no este). Logic Apps (plan Consumption) sí se puede crear
# en northcentralus, la misma región de la VM.
#
# Por qué identidad administrada en vez de OIDC/service principal clásico:
# el tenant de Entra ID de este proyecto (UCompensar) tiene
# allowedToCreateApps=false (verificado con
# az rest --method get --url "https://graph.microsoft.com/v1.0/policies/authorizationPolicy"
# --query "defaultUserRolePermissions.allowedToCreateApps" -o tsv) y el
# usuario no tiene ningún rol de directorio elevado (verificado con
# az rest --method get --url "https://graph.microsoft.com/v1.0/me/transitiveMemberOf/microsoft.graph.directoryRole"
# --query "value[].displayName" -o tsv, vacío). Sin poder crear un App
# Registration, ni OIDC federado ni "az ad sp create-for-rbac" son viables:
# los dos necesitan crear un App Registration. Una identidad administrada de
# un Logic App no tiene esa restricción: la crea el propio recurso de
# Azure, no Entra ID. Cada Logic App tiene su PROPIA identidad (dos
# principalId distintos, uno por recurso): a diferencia de la cuenta de
# Automation compartida del diseño anterior, acá no hay un solo principal
# que cubra los dos horarios.
#
# Por qué dos Logic Apps propios y no la solución empaquetada "Start/Stop
# VMs v2" de Microsoft: esa solución agrega Function App, Storage Account,
# otro Logic App y Application Insights, pensada para flotas de VMs en
# varias suscripciones. Para una sola VM es sobre-ingeniería.
#
# Secuencia obligatoria al crear o habilitar un Logic App: Disabled -> rol
# "Virtual Machine Contributor" sobre la VM -> esperar propagación de RBAC
# -> Enabled, y además, una definición con "startTime" resuelto en el
# futuro. Las dos son defensas contra la MISMA familia de problema (una
# corrida automática disparada fuera de lo esperado) pero para dos causas
# distintas, las dos verificadas en vivo hoy: sin el orden Disabled->rol,
# la primera corrida falla con Forbidden; sin "startTime", crear, habilitar
# o incluso actualizar la definición de un Logic App ya Enabled dispara una
# corrida de inmediato, sin importar la hora programada. Ver los
# comentarios de asegurar_estado_habilitado_logic_app y
# calcular_start_time_logic_app para el detalle de cada una.
#
# Corre en el portátil del operador, igual que infra/azure/crear-servidor.sh
# (ver la cabecera de ese archivo): nunca en el servidor real.
#
# Banderas: --simular y --ayuda. A diferencia de crear-servidor.sh, este
# script NO tiene --registrar-proveedores: la tarjeta no lo pidió. Los
# proveedores Microsoft.Automation y Microsoft.Logic quedaron Registered en
# esta suscripción como efecto colateral de la sesión de hoy (el primero,
# del intento fallido con Automation Account; el segundo, del despliegue
# real de Logic Apps), así que no hace falta registrarlos para lo que este
# script hace hoy. Si algún día se necesitara en una suscripción nueva,
# sigue siendo decisión de un humano (az provider register -n <proveedor>
# --wait), no algo que este script automatice en silencio: es un cambio a
# nivel de suscripción que comparte el equipo.
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

mostrar_ayuda() {
  cat <<'EOF'
Uso: automatizar-horario.sh [opciones]

Crea en Azure la automatización de encendido/apagado diario de la VM de
Centinela (tarjeta SCRUM-29): dos Logic Apps (plan Consumption), cada uno
con identidad administrada system-assigned, el rol "Virtual Machine
Contributor" sobre la VM, un trigger de Recurrence diario y una acción HTTP
contra Azure Resource Manager para encender o apagar (deallocate)
centinela-vm. Lee su configuración de infra/azure/variables.sh y las
definiciones de infra/azure/logic-apps/*.json.

Opciones:
  --simular   Imprime lo que haría, sin ejecutar ningún "az" que escriba.
              Las lecturas sí corren, para que la simulación refleje el
              estado real de la suscripción. En una corrida --simular contra
              una suscripción donde todavía no existe alguno de los dos
              Logic Apps, los pasos que dependen de leer su principalId o su
              definición publicada no tienen un valor real que leer: en vez
              de fallar, avisan que ese dato solo se conoce en una corrida
              real y siguen adelante.
  --ayuda     Muestra esta ayuda.

Sin opciones, corre la verificación previa y crea (o actualiza) los
recursos que falten o que hayan cambiado.

Cada Logic App se crea primero en --state Disabled, con su identidad
administrada ya habilitada; solo después de confirmar (con reintento) que
el rol "Virtual Machine Contributor" sobre la VM ya se propagó a una
lectura, este script lo pasa a --state Enabled (un Logic App Enabled sin
rol propagado dispara una corrida inmediata con Forbidden, verificado en
vivo). La definición incluye además un "startTime" futuro: sin él, crear,
habilitar o actualizar el recurso dispara una corrida inmediata fuera de
horario, incluso con el rol ya en su lugar (verificado en vivo por
separado). Por eso mismo, la comparación con jq que decide si hace falta un
"az logic workflow update" ignora tanto los campos calculados que Azure
agrega al guardar (evaluatedRecurrence) como el propio "startTime" (siempre
"la próxima ocurrencia futura" calculada hoy): compararlos sin más haría
que el script creyera que la definición "cambió" en cada corrida.

Variables de entorno relevantes:
  CONFIRMAR_SUSCRIPCION=si  Salta la confirmación interactiva de la
                             suscripción activa. Úsala solo si ya la
                             verificaste por otro medio.

Requiere "az login" vigente y "jq" en el PATH, incluso en modo --simular.

No incluye ningún camino de rollback ni de teardown: borrar recursos en la
nube necesita que lo decida un humano.
EOF
}

# jq no es una dependencia genérica de infra/azure/ (crear-servidor.sh no lo
# necesita), así que el chequeo vive acá, no en lib/comun.sh: es específico
# de cómo este script decide si hace falta un "az logic workflow update"
# (ver asegurar_definicion_logic_app) sin falsos positivos.
verificar_cli_jq() {
  command -v jq >/dev/null 2>&1 \
    || error_fatal "No se encontró jq en el PATH. Es necesario para comparar la definición publicada de cada Logic App sin falsos positivos por los campos calculados que Azure le agrega (evaluatedRecurrence) ni por startTime, que cambia en cada corrida."
}

verificar_preflight() {
  paso "Verificación previa"
  verificar_cli_az
  verificar_cli_jq
  confirmar_suscripcion
}

# Devuelve (por stdout) el epoch Unix de la próxima ocurrencia de "hora_utc"
# (formato "HH:MM"): hoy si esa hora todavía no pasó, mañana si ya pasó. La
# usa calcular_start_time_logic_app para construir el "startTime" del
# trigger de Recurrence de cada Logic App: un startTime en el pasado no
# evitaría el disparo inmediato que se busca prevenir, así que cada corrida
# de este script tiene que recalcularlo, para que sea re-ejecutable sin
# fallar por una fecha vieja.
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

# Devuelve (por stdout) el "startTime" que exige el bloque "recurrence" de
# un trigger de Recurrence de Logic Apps para la próxima ocurrencia de
# "hora_utc": la hora local de Bogotá, calculada a partir del mismo epoch
# que ya resuelve calcular_proxima_ocurrencia.
#
# Un trigger de Recurrence sin "startTime" dispara una corrida de inmediato
# al crearse, al habilitarse o al actualizar su definición (verificado en
# vivo hoy contra centinela-apagar, muy lejos de la hora programada), así
# que este campo no es opcional acá.
#
# Formato SIN offset (sin "%:z"), A PROPÓSITO: cuando el trigger ya trae
# "timeZone" fijado (ver las plantillas de infra/azure/logic-apps/), Azure
# rechaza un startTime con offset embebido con
# "InvalidWorkflowTriggerRecurrence" (verificado en vivo:
# "2026-09-03T12:00:00-05:00" rechazado, "2026-09-03T12:00:00" aceptado).
# Es lo opuesto de lo que exige "--start-time" en "az automation schedule
# create" para Automation Account (que sí necesita el offset embebido,
# porque ahí no hay un "timeZone" fijado de antemano de la misma forma): no
# "arreglar" esto copiando el patrón del otro lado, son dos reglas
# opuestas, cada una verificada por separado.
calcular_start_time_logic_app() {
  local hora_utc="$1" epoch_proximo

  epoch_proximo="$(calcular_proxima_ocurrencia "$hora_utc")"
  TZ="America/Bogota" date -d "@${epoch_proximo}" +%Y-%m-%dT%H:%M:%S
}

# Sustituye los placeholders literales del JSON de plantilla ({SUSCRIPCION},
# {GRUPO_RECURSOS}, {NOMBRE_VM}, {START_TIME}) por los valores reales de
# esta corrida y deja el resultado en un archivo temporal SIN envolver (el
# objeto crudo del workflow: $schema/contentVersion/triggers/actions/
# outputs). Se resuelve con expansión de parámetros de bash, no con jq: los
# placeholders viven dentro de valores de cadena (la URI, el startTime), no
# son una estructura que jq tendría que reconstruir. El archivo de
# plantilla en infra/azure/logic-apps/ NUNCA se edita en el disco: solo se
# lee, la sustitución vive en memoria. Sufijo ".json" en el archivo
# temporal por prolijidad, no por necesidad real del CLI.
#
# Este archivo SIN envolver es el que compara asegurar_definicion_logic_app
# contra "az logic workflow show --query definition" (que también devuelve
# el objeto sin envolver, porque ahí "definition" ya es el nombre de la
# propiedad). NO se lo pases directo a "--definition": verificado en vivo
# que esta versión instalada del CLI (extensión "logic" 1.1.0) rechaza un
# archivo sin envolver con "--definition does not contain a 'definition'
# key". Para eso existe envolver_definicion_logic_app, que envuelve ESTE
# archivo en uno aparte justo antes de cada "create"/"update".
resolver_definicion_logic_app() {
  local archivo_plantilla="$1" hora_utc="$2"
  local suscripcion contenido start_time archivo_salida

  suscripcion="$(az account show --query id -o tsv --only-show-errors)" \
    || error_fatal "No se pudo leer el id de la suscripción activa."
  start_time="$(calcular_start_time_logic_app "$hora_utc")"

  contenido="$(cat "$archivo_plantilla")"
  contenido="${contenido//\{SUSCRIPCION\}/$suscripcion}"
  contenido="${contenido//\{GRUPO_RECURSOS\}/$GRUPO_RECURSOS}"
  contenido="${contenido//\{NOMBRE_VM\}/$NOMBRE_VM}"
  contenido="${contenido//\{START_TIME\}/$start_time}"

  archivo_salida="$(mktemp --suffix=.json)"
  printf '%s' "$contenido" >"$archivo_salida"
  printf '%s' "$archivo_salida"
}

# Envuelve el archivo SIN envolver de resolver_definicion_logic_app en uno
# nuevo con la clave literal "definition" al nivel superior, en un archivo
# temporal aparte (nunca modifica archivo_sin_envolver). Es el único
# formato que esta versión instalada del CLI acepta para
# "az logic workflow create/update --definition" (verificado en vivo,
# az-cli 2.89.1 con la extensión "logic" 1.1.0: sin envolver, el comando
# falla ANTES de llamar la API con "--definition does not contain a
# 'definition' key"; con --slurpfile de abajo, verificado que sí funciona
# tanto para "create" como para "update" contra un Logic App real
# descartable, borrado después de la prueba). No envolver también el
# archivo de plantilla en infra/azure/logic-apps/ ni el que produce
# resolver_definicion_logic_app: asegurar_definicion_logic_app necesita esa
# versión sin envolver para compararla contra "az logic workflow show
# --query definition", que devuelve el objeto YA sin envolver.
envolver_definicion_logic_app() {
  local archivo_sin_envolver="$1"
  local archivo_envuelto

  archivo_envuelto="$(mktemp --suffix=.json)"
  jq -n --slurpfile def "$archivo_sin_envolver" '{definition: $def[0]}' >"$archivo_envuelto" \
    || error_fatal "No se pudo envolver la definición de ${archivo_sin_envolver} para pasarla a --definition."

  printf '%s' "$archivo_envuelto"
}

# Crea el Logic App si no existe todavía, en --state Disabled y con
# identidad administrada system-assigned habilitada desde la creación. Se
# crea Disabled a propósito, nunca Enabled: ver la cabecera del archivo y
# asegurar_estado_habilitado_logic_app para el detalle completo de por qué
# (Forbidden por RBAC sin propagar, y por separado, disparo inmediato sin
# startTime).
asegurar_logic_app_recurso() {
  local nombre="$1" archivo_definicion="$2"
  local archivo_envuelto

  paso "Recurso: ${nombre}"

  if recurso_existe logic workflow show -g "$GRUPO_RECURSOS" -n "$nombre"; then
    registrar_existente "El Logic App ${nombre}"
    return 0
  fi

  archivo_envuelto="$(envolver_definicion_logic_app "$archivo_definicion")"
  ejecutar_cambio "el Logic App ${nombre} (Disabled, con identidad administrada)" \
    az logic workflow create -g "$GRUPO_RECURSOS" -n "$nombre" -l "$REGION" \
    --definition "$archivo_envuelto" --state Disabled --mi-system-assigned true
  rm -f "$archivo_envuelto"
}

# Lee (con reintento) el principalId de la identidad administrada del Logic
# App. Mismo patrón de reintento (9 intentos/10s) que usaba
# asegurar_identidad_administrada para el Automation Account del diseño
# anterior: la propagación a Microsoft Graph/RBAC de una identidad recién
# creada puede tardar unos segundos, y sin reintento la primera lectura
# justo después de crear el recurso suele devolver vacío.
leer_principal_id_logic_app() {
  local nombre="$1" principal_id intento

  if ! recurso_existe logic workflow show -g "$GRUPO_RECURSOS" -n "$nombre"; then
    # Solo pasa bajo --simular contra un Logic App que todavía no existe de
    # verdad: no hay un principalId real que leer todavía.
    return 0
  fi

  for intento in $(seq 1 9); do
    principal_id="$(az logic workflow show -g "$GRUPO_RECURSOS" -n "$nombre" --query identity.principalId -o tsv --only-show-errors 2>/dev/null || true)"
    if [[ -n "$principal_id" ]]; then
      printf '%s' "$principal_id"
      return 0
    fi
    info "Esperando a que se propague el principalId de ${nombre} (intento ${intento}/9)..."
    sleep 10
  done

  error_fatal "La identidad administrada de ${nombre} no propagó un principalId después de 90s."
}

# A diferencia de "az role assignment create", que sí acepta
# --assignee-principal-type, "az role assignment list" lo RECHAZA con
# "unrecognized arguments" (verificado en este entorno, az-cli 2.89.1): por
# eso el chequeo de existencia de abajo usa solo --assignee-object-id, sin
# ese flag, aunque la creación sí lo use. Este es un ajuste sobre el patrón
# del diseño anterior (asegurar_rol_vm para Automation Account), que sí
# combinaba los dos flags en el "list" y por eso el chequeo de existencia
# ahí nunca funcionó de verdad (el "2>/dev/null || true" que lo rodeaba
# ocultaba el error, así que ese script habría reintentado crear el rol en
# cada corrida sin fallar nunca lo bastante fuerte como para notarlo).
asegurar_rol_logic_app() {
  local nombre="$1" principal_id="$2"
  local id_vm asignacion_existente

  paso "Rol 'Virtual Machine Contributor' de ${nombre} sobre ${NOMBRE_VM}"

  if [[ -z "$principal_id" ]]; then
    info "[simular] Sin un principalId real todavía no se puede simular este paso."
    return 0
  fi

  id_vm="$(az vm show -g "$GRUPO_RECURSOS" -n "$NOMBRE_VM" --query id -o tsv --only-show-errors)" \
    || error_fatal "No se pudo leer el id de la VM ${NOMBRE_VM}."

  asignacion_existente="$(az role assignment list --assignee-object-id "$principal_id" \
    --scope "$id_vm" --role "Virtual Machine Contributor" --query "[0].id" -o tsv --only-show-errors 2>/dev/null || true)"

  if [[ -n "$asignacion_existente" ]]; then
    registrar_existente "El rol 'Virtual Machine Contributor' de ${nombre} sobre ${NOMBRE_VM}"
    return 0
  fi

  ejecutar_cambio "el rol 'Virtual Machine Contributor' de ${nombre} sobre ${NOMBRE_VM}" \
    az role assignment create --assignee-object-id "$principal_id" --assignee-principal-type ServicePrincipal \
    --role "Virtual Machine Contributor" --scope "$id_vm"

  [[ "$SIMULAR" == "si" ]] && return 0

  esperar_propagacion_rol "$principal_id" "$id_vm"
}

# Reintento acotado (9 intentos/10s, mismo patrón que
# leer_principal_id_logic_app) para confirmar que el rol recién asignado ya
# es una lectura estable antes de habilitar el Logic App: habilitar
# demasiado pronto es justo lo que produjo el Forbidden observado en la
# prueba en vivo de hoy (ver la cabecera del archivo).
esperar_propagacion_rol() {
  local principal_id="$1" id_vm="$2"
  local intento asignacion

  for intento in $(seq 1 9); do
    asignacion="$(az role assignment list --assignee-object-id "$principal_id" \
      --scope "$id_vm" --role "Virtual Machine Contributor" --query "[0].id" -o tsv --only-show-errors 2>/dev/null || true)"
    [[ -n "$asignacion" ]] && return 0
    info "Esperando a que se propague el rol asignado (intento ${intento}/9)..."
    sleep 10
  done

  error_fatal "El rol 'Virtual Machine Contributor' no se propagó a una lectura después de 90s."
}

# Compara solo el subconjunto de la definición que de verdad importa
# (frequency, interval, timeZone, schedule.hours y actions), no el objeto
# completo: Azure agrega campos calculados al guardar la definición de un
# Logic App (por ejemplo triggers.Recurrence.evaluatedRecurrence), y
# "startTime" cambia en cada corrida porque siempre es "la próxima
# ocurrencia futura" calculada hoy (ver calcular_start_time_logic_app). Una
# comparación cruda de cualquiera de los dos SIEMPRE diría "distinto" y
# reintentaría la escritura en cada corrida, incluso sin cambios reales.
asegurar_definicion_logic_app() {
  local nombre="$1" archivo_definicion="$2"
  local relevante_local relevante_remoto archivo_envuelto

  paso "Definición: ${nombre}"

  if ! recurso_existe logic workflow show -g "$GRUPO_RECURSOS" -n "$nombre"; then
    info "[simular] Sin un Logic App real todavía no se puede comparar su definición."
    return 0
  fi

  relevante_local="$(jq -S '{recurrence: (.triggers.Recurrence.recurrence | del(.startTime)), actions: .actions}' "$archivo_definicion")"
  relevante_remoto="$(az logic workflow show -g "$GRUPO_RECURSOS" -n "$nombre" --query definition -o json --only-show-errors \
    | jq -S '{recurrence: (.triggers.Recurrence.recurrence | del(.startTime)), actions: .actions}')" \
    || error_fatal "No se pudo leer la definición publicada de ${nombre}."

  if [[ "$relevante_local" == "$relevante_remoto" ]]; then
    info "La definición relevante de ${nombre} ya coincide (sin contar startTime, ver arriba)."
    return 0
  fi

  # Un "update" de la definición de un Logic App ya Enabled podría, en
  # teoría, volver a disparar el trigger igual que crear o habilitar: se
  # verificó en vivo que el startTime futuro que ya trae archivo_definicion
  # también cubre este caso (ninguna corrida nueva apareció al actualizar
  # centinela-apagar con su startTime puesto), pero queda documentado acá
  # porque es la única defensa real contra eso, no una garantía aparte.
  archivo_envuelto="$(envolver_definicion_logic_app "$archivo_definicion")"
  ejecutar_cambio "la definición del Logic App ${nombre}" \
    az logic workflow update -g "$GRUPO_RECURSOS" -n "$nombre" --definition "$archivo_envuelto"
  rm -f "$archivo_envuelto"
}

# Último paso de la secuencia Disabled -> rol -> Enabled: solo llega acá
# después de que asegurar_rol_logic_app confirmó (con reintento) que el rol
# ya es una lectura estable, así que habilitar acá no repite el Forbidden
# de la prueba en vivo de hoy. Ese orden por sí solo, sin embargo, NO basta
# para evitar que el Logic App ejecute su acción real fuera de horario: se
# verificó en vivo que habilitar un Logic App (no solo crearlo) también
# dispara su trigger de inmediato si la definición no trae "startTime". La
# defensa real contra eso es el startTime futuro que ya viene resuelto en
# archivo_definicion desde que se creó o actualizó (ver
# calcular_start_time_logic_app y asegurar_definicion_logic_app), no el
# orden Disabled->Enabled de esta función.
asegurar_estado_habilitado_logic_app() {
  local nombre="$1" principal_id="$2"
  local estado_actual

  paso "Estado: ${nombre}"

  if [[ -z "$principal_id" ]]; then
    info "[simular] Sin un principalId real todavía no se puede simular este paso."
    return 0
  fi

  estado_actual="$(az logic workflow show -g "$GRUPO_RECURSOS" -n "$nombre" --query state -o tsv --only-show-errors)" \
    || error_fatal "No se pudo leer el estado de ${nombre}."

  if [[ "$estado_actual" == "Enabled" ]]; then
    registrar_existente "El estado Enabled del Logic App ${nombre}"
    return 0
  fi

  ejecutar_cambio "habilitar el Logic App ${nombre}" \
    az logic workflow update -g "$GRUPO_RECURSOS" -n "$nombre" --state Enabled
}

# Orquesta la creación/actualización idempotente de un Logic App: recurso
# (Disabled + identidad) -> rol sobre la VM -> definición -> habilitado. Ver
# el comentario de cada asegurar_* para el porqué de cada paso y del orden.
asegurar_logic_app() {
  local nombre="$1" archivo_plantilla="$2" hora_utc="$3"
  local archivo_definicion principal_id

  paso "Logic App: ${nombre}"

  archivo_definicion="$(resolver_definicion_logic_app "$archivo_plantilla" "$hora_utc")"

  asegurar_logic_app_recurso "$nombre" "$archivo_definicion"

  principal_id="$(leer_principal_id_logic_app "$nombre")"
  asegurar_rol_logic_app "$nombre" "$principal_id"

  asegurar_definicion_logic_app "$nombre" "$archivo_definicion"
  asegurar_estado_habilitado_logic_app "$nombre" "$principal_id"

  # Limpieza del archivo temporal de resolver_definicion_logic_app: es un
  # archivo propio de este script, creado y consumido en la misma corrida,
  # no un archivo del usuario, así que un "rm -f" directo es lo correcto
  # acá (a diferencia de un borrado interactivo, que en este equipo pasa
  # por trash-put).
  rm -f "$archivo_definicion"
}

crear_recursos() {
  # 13:00 UTC = 08:00 COT (encendido) · 17:00 UTC = 12:00 COT (apagado). El
  # mismo hora_utc alimenta el "startTime" (calcular_start_time_logic_app);
  # el "schedule.hours" (8 y 12, hora de Bogotá) ya está fijo en cada
  # plantilla JSON. Si el horario cambia algún día, los dos tienen que
  # actualizarse juntos.
  asegurar_logic_app "$NOMBRE_LOGIC_ENCENDER" "${DIRECTORIO_SCRIPT}/logic-apps/encender-vm.json" "13:00"
  asegurar_logic_app "$NOMBRE_LOGIC_APAGAR" "${DIRECTORIO_SCRIPT}/logic-apps/apagar-vm.json" "17:00"
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
