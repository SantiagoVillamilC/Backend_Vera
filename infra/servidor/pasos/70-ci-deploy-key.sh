#!/usr/bin/env bash
# infra/servidor/pasos/70-ci-deploy-key.sh
# Mantiene al día el command= forzado de la llave de deploy de CI (SCRUM-100).
#
# authorized_keys traía una lista fija de "-f compose*.yaml" (los tres que
# existían cuando se generó la llave a mano, ver bitácora 2026-09-07), en vez
# del glob dinámico de asegurar_servicio_compose: por eso "up -d api" seguía
# fallando con "no such service: api" aunque compose.api.yaml ya existiera
# (bitácora 2026-09-24). No genera el par de llaves ni instala la pública, eso
# sigue siendo manual; se omite con un aviso si la línea no existe todavía.

NOMBRE_PASO="Actualizar el command= forzado de la llave de deploy de CI"
TARJETA="SCRUM-100"

paso "${NOMBRE_PASO} (${TARJETA})"

ARCHIVO_AUTHORIZED_KEYS="/home/centinela/.ssh/authorized_keys"
MARCADOR_LLAVE="github-actions-deploy@backend-centinela"

if [[ ! -f "${ARCHIVO_AUTHORIZED_KEYS}" ]]; then
  aviso "No existe ${ARCHIVO_AUTHORIZED_KEYS} en este sistema: se omite (esperado en Vagrant o en probar-en-docker.sh)."
  return 0
fi

if ! grep -qF "${MARCADOR_LLAVE}" "${ARCHIVO_AUTHORIZED_KEYS}"; then
  aviso "No se encontró ninguna línea con el marcador \"${MARCADOR_LLAVE}\" en ${ARCHIVO_AUTHORIZED_KEYS}: se omite. La llave de deploy se instala a mano la primera vez (ver bitácora técnica 2026-09-07)."
  return 0
fi

# Mismo glob que asegurar_servicio_compose, pero con nombres relativos
# (el command= ya hace "cd ${DIRECTORIO_SCRIPT}" primero).
# El comando forzado ya no lleva los compose dentro: llama a deploy-ci.sh, que
# recibe la rama por SSH_ORIGINAL_COMMAND y comprueba que sea dev, staging o
# main. Así el mismo comando despliega las tres ramas sin reescribir esta
# línea, que fue el motivo por el que "up -d api" quedó fijo en :main.
ARCHIVO_DEPLOY_CI="${DIRECTORIO_SCRIPT}/deploy-ci.sh"
if [[ ! -f "${ARCHIVO_DEPLOY_CI}" ]]; then
  error_fatal "No existe ${ARCHIVO_DEPLOY_CI}: la llave de deploy apuntaría a un archivo ausente y el CD fallaría en cada push."
fi
# El comando forzado corre como centinela (sshd no hace sudo por su cuenta), así
# que el script tiene que ser ejecutable por cualquiera. Lo que impide que
# centinela lo edite es el dueño, no el modo: root:root y sin bit de escritura.
chown root:root "${ARCHIVO_DEPLOY_CI}"
chmod 755 "${ARCHIVO_DEPLOY_CI}"

comando_forzado="${ARCHIVO_DEPLOY_CI}"

linea_actual="$(grep -F "${MARCADOR_LLAVE}" "${ARCHIVO_AUTHORIZED_KEYS}")"

# La línea completa es "command=\"...\",opciones... ssh-ed25519 AAAA... marcador".
# "#command=\"*\"," quita, con la coincidencia más corta posible del glob "*",
# todo hasta la primera "\"," de cierre: lo que sigue (opciones, tipo de
# llave, material, comentario) se preserva textual, solo se reconstruye el
# contenido entre comillas.
sufijo="${linea_actual#command=\"*\",}"

linea_nueva="command=\"${comando_forzado}\",${sufijo}"

if [[ "${linea_actual}" == "${linea_nueva}" ]]; then
  info "El command= forzado de la llave de deploy ya apunta a ${ARCHIVO_DEPLOY_CI}."
else
  # sshd exige que authorized_keys sea dueño del usuario (StrictModes): este paso corre
  # como root vía aprovisionar.sh, así que mktemp/mv heredarían root:root sin este chown
  # explícito, y sshd rechazaría la llave con "Permission denied (publickey)" aunque el
  # contenido quedara correcto (bloqueó el CD real el 24 de sep, ver bitácora técnica).
  dueno_original="$(stat -c '%U:%G' "${ARCHIVO_AUTHORIZED_KEYS}")"
  archivo_temporal="$(mktemp "${ARCHIVO_AUTHORIZED_KEYS}.XXXXXX")"
  # -F: MARCADOR_LLAVE no es un patrón regex, es el texto literal del marcador.
  grep -vF "${MARCADOR_LLAVE}" "${ARCHIVO_AUTHORIZED_KEYS}" > "${archivo_temporal}"
  printf '%s\n' "${linea_nueva}" >> "${archivo_temporal}"
  chown "${dueno_original}" "${archivo_temporal}"
  chmod 600 "${archivo_temporal}"
  mv "${archivo_temporal}" "${ARCHIVO_AUTHORIZED_KEYS}"
  info "Se actualizó el command= forzado para llamar a ${ARCHIVO_DEPLOY_CI}."
  registrar_cambio
fi
