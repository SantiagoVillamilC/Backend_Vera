#!/usr/bin/env bash
# infra/azure/automatizar-horario.sh
# SCRUM-29: crea un Logic App en Azure para reiniciar centinela-vm a diario
# (05:00 hora Colombia). La VM corre 24/7, este script no la apaga. Corre
# en el portátil del operador.
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

Crea en Azure el reinicio diario de la VM de Centinela (tarjeta SCRUM-29):
un Logic App (plan Consumption) con identidad administrada
system-assigned, el rol "Virtual Machine Contributor" sobre la VM, un
trigger de Recurrence diario y una acción HTTP contra Azure Resource
Manager para reiniciar (restart) centinela-vm. La VM corre 24/7, este
script no la apaga. Lee su configuración de infra/azure/variables.sh y la
definición de infra/azure/logic-apps/reiniciar-vm.json.

Opciones:
  --simular   Imprime lo que haría, sin ejecutar ningún "az" que escriba.
              Las lecturas sí corren, para que la simulación refleje el
              estado real de la suscripción. En una corrida --simular contra
              una suscripción donde todavía no existe el Logic App, los
              pasos que dependen de leer su principalId o su definición
              publicada no tienen un valor real que leer: en vez de fallar,
              avisan que ese dato solo se conoce en una corrida real y
              siguen adelante.
  --ayuda     Muestra esta ayuda.

Sin opciones, corre la verificación previa y crea (o actualiza) los
recursos que falten o que hayan cambiado.

El Logic App se crea primero en --state Disabled, con su identidad
administrada ya habilitada; solo después de confirmar (con reintento) que
el rol "Virtual Machine Contributor" sobre la VM ya se propagó a una
lectura, este script lo pasa a --state Enabled (un Logic App Enabled sin
rol propagado dispara una corrida inmediata con Forbidden, verificado en
vivo). La definición incluye además un "startTime" fijo en el pasado
(constante, la misma en cada corrida) más "minutes" explícito en
"schedule": sin "startTime", crear, habilitar o actualizar el recurso
dispara una corrida inmediata fuera de horario (verificado en vivo por
separado), y si "startTime" coincidiera EXACTO con una hora de
"schedule.hours", Azure salta esa ocurrencia y agenda el disparo un día
después (también verificado en vivo, ver la bitácora). Con la VM 24/7 ese
disparo inmediato ya no prende ni apaga fuera de horario: reinicia
producción a mitad de una request, por eso el startTime fijo sigue siendo
la defensa real. Por lo mismo, la comparación con jq que decide si hace
falta un "az logic workflow update" ignora solo los campos calculados que
Azure agrega al guardar (evaluatedRecurrence): con "startTime" fijo, sí se
compara, para que el diff detecte también si alguien cambia ese valor por
error.

Variables de entorno relevantes:
  CONFIRMAR_SUSCRIPCION=si  Salta la confirmación interactiva de la
                             suscripción activa. Úsala solo si ya la
                             verificaste por otro medio.

Requiere "az login" vigente y "jq" en el PATH, incluso en modo --simular.

No incluye ningún camino de rollback ni de teardown: borrar recursos en la
nube necesita que lo decida un humano.
EOF
}

# jq no es dependencia genérica de infra/azure/ (crear-servidor.sh no lo
# necesita): es específico de este script.
verificar_cli_jq() {
  command -v jq >/dev/null 2>&1 \
    || error_fatal "No se encontró jq en el PATH. Es necesario para comparar la definición publicada de cada Logic App sin falsos positivos por los campos calculados que Azure le agrega (evaluatedRecurrence)."
}

verificar_preflight() {
  paso "Verificación previa"
  verificar_cli_az
  verificar_cli_jq
  confirmar_suscripcion
}

# Sustituye placeholders con expansión de bash (nunca edita la plantilla en
# disco). Queda SIN envolver: usa envolver_definicion_logic_app antes de --definition.
resolver_definicion_logic_app() {
  local archivo_plantilla="$1"
  local suscripcion contenido archivo_salida

  suscripcion="$(az account show --query id -o tsv --only-show-errors)" \
    || error_fatal "No se pudo leer el id de la suscripción activa."

  contenido="$(cat "$archivo_plantilla")"
  contenido="${contenido//\{SUSCRIPCION\}/$suscripcion}"
  contenido="${contenido//\{GRUPO_RECURSOS\}/$GRUPO_RECURSOS}"
  contenido="${contenido//\{NOMBRE_VM\}/$NOMBRE_VM}"

  archivo_salida="$(mktemp --suffix=.json)"
  printf '%s' "$contenido" >"$archivo_salida"
  printf '%s' "$archivo_salida"
}

# Envuelve en {"definition": ...}: el CLI lo exige en --definition, si no
# falla con "does not contain a 'definition' key".
envolver_definicion_logic_app() {
  local archivo_sin_envolver="$1"
  local archivo_envuelto

  archivo_envuelto="$(mktemp --suffix=.json)"
  jq -n --slurpfile def "$archivo_sin_envolver" '{definition: $def[0]}' >"$archivo_envuelto" \
    || error_fatal "No se pudo envolver la definición de ${archivo_sin_envolver} para pasarla a --definition."

  printf '%s' "$archivo_envuelto"
}

# Crea en --state Disabled, nunca Enabled: habilitar antes de que el rol
# se propague produce Forbidden.
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

# Reintento (9 intentos/10s): el principalId recién creado puede tardar
# unos segundos en propagar a RBAC.
leer_principal_id_logic_app() {
  local nombre="$1" principal_id intento

  if ! recurso_existe logic workflow show -g "$GRUPO_RECURSOS" -n "$nombre"; then
    # Bajo --simular sin un Logic App real, no hay principalId que leer.
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

# "az role assignment list" rechaza --assignee-principal-type (a diferencia
# de "create"): el chequeo de abajo usa solo --assignee-object-id.
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

# Reintento (9 intentos/10s): confirma que el rol ya es una lectura
# estable antes de habilitar, o produce Forbidden.
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

# Compara solo recurrence+actions, no el objeto completo: Azure agrega
# campos calculados (evaluatedRecurrence) que darían siempre "distinto".
asegurar_definicion_logic_app() {
  local nombre="$1" archivo_definicion="$2"
  local relevante_local relevante_remoto archivo_envuelto

  paso "Definición: ${nombre}"

  if ! recurso_existe logic workflow show -g "$GRUPO_RECURSOS" -n "$nombre"; then
    info "[simular] Sin un Logic App real todavía no se puede comparar su definición."
    return 0
  fi

  relevante_local="$(jq -S '{recurrence: .triggers.Recurrence.recurrence, actions: .actions}' "$archivo_definicion")"
  relevante_remoto="$(az logic workflow show -g "$GRUPO_RECURSOS" -n "$nombre" --query definition -o json --only-show-errors \
    | jq -S '{recurrence: .triggers.Recurrence.recurrence, actions: .actions}')" \
    || error_fatal "No se pudo leer la definición publicada de ${nombre}."

  if [[ "$relevante_local" == "$relevante_remoto" ]]; then
    info "La definición relevante de ${nombre} ya coincide."
    return 0
  fi

  # Un update sobre un Logic App ya Enabled podría re-disparar el trigger;
  # el startTime fijo es la defensa real.
  archivo_envuelto="$(envolver_definicion_logic_app "$archivo_definicion")"
  ejecutar_cambio "la definición del Logic App ${nombre}" \
    az logic workflow update -g "$GRUPO_RECURSOS" -n "$nombre" --definition "$archivo_envuelto"
  rm -f "$archivo_envuelto"
}

# Último paso Disabled -> rol -> Enabled: evita Forbidden, pero la defensa
# real contra un disparo fuera de horario es el startTime fijo de la plantilla.
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

# Orquesta un Logic App: recurso (Disabled+identidad) -> rol -> definición
# -> habilitado.
asegurar_logic_app() {
  local nombre="$1" archivo_plantilla="$2"
  local archivo_definicion principal_id

  paso "Logic App: ${nombre}"

  archivo_definicion="$(resolver_definicion_logic_app "$archivo_plantilla")"

  asegurar_logic_app_recurso "$nombre" "$archivo_definicion"

  principal_id="$(leer_principal_id_logic_app "$nombre")"
  asegurar_rol_logic_app "$nombre" "$principal_id"

  asegurar_definicion_logic_app "$nombre" "$archivo_definicion"
  asegurar_estado_habilitado_logic_app "$nombre" "$principal_id"

  # Archivo temporal propio del script: "rm -f" directo está bien acá, no
  # pasa por trash-put (no es un archivo de usuario).
  rm -f "$archivo_definicion"
}

crear_recursos() {
  # 05:00 hora Colombia ya viene fijo en la plantilla JSON. Para cambiar el
  # horario, edita la plantilla en logic-apps/.
  asegurar_logic_app "$NOMBRE_LOGIC_REINICIAR" "${DIRECTORIO_SCRIPT}/logic-apps/reiniciar-vm.json"
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
