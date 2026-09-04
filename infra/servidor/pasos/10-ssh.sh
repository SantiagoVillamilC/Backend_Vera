#!/usr/bin/env bash
# infra/servidor/pasos/10-ssh.sh
# Endurece SSH (SCRUM-28): puerto de red-fijada.sh, solo autenticación por
# llave pública. Se carga con source: nunca "return" salvo dentro de
# _ssh10_listar_declaraciones (ver cabecera de 00-verificar-sistema.sh).

NOMBRE_PASO="Endurecer el acceso por SSH"
TARJETA="SCRUM-28"

paso "${NOMBRE_PASO} (${TARJETA})"

# Ayuda de diagnóstico: busca (no parsea) qué archivo declara la directiva,
# para nombrarlo en el error. sshd -T más abajo decide de verdad.
_ssh10_listar_declaraciones() {
  local directiva="$1"
  local archivo
  for archivo in /etc/ssh/sshd_config.d/*.conf /etc/ssh/sshd_config; do
    [[ -f "$archivo" ]] || continue
    while IFS= read -r linea; do
      printf '    %s: %s\n' "$archivo" "$linea"
    done < <(grep -iE "^[[:space:]]*${directiva}[[:space:]]" "$archivo" || true)
  done
}

if [[ ! -d /run/systemd/system ]]; then
  # No basta "command -v systemctl" (mismo criterio que asegurar_servicio_activo).
  # Sin systemd como proceso 1 se omite acá y se verifica en la VM real.
  aviso "systemd no es el proceso 1 en este sistema (no existe /run/systemd/system): se omite por completo el endurecimiento de SSH. Verifícalo en la VM real (Azure o Vagrant)."
else
  # Fatal, no solo aviso: con ssh.socket activo, Port se ignora en silencio
  # y el 22 seguiría abierto aunque este paso reporte éxito.
  if systemctl is-enabled ssh.socket &>/dev/null; then
    error_fatal "ssh.socket está habilitado. Con activación por socket la directiva Port de sshd_config se ignora en silencio y el servidor seguiría escuchando en el puerto 22. Deshabilítalo primero con: systemctl disable --now ssh.socket"
  fi
  info "ssh.socket no está habilitado, como se esperaba: la directiva Port de sshd_config sí va a tener efecto."

  archivo_config_ssh="/etc/ssh/sshd_config.d/10-centinela.conf"

  asegurar_directorio "$(dirname "${archivo_config_ssh}")"

  cambios_antes="$(resumen_cambios)"

  asegurar_linea_en_archivo "${archivo_config_ssh}" "Port ${PUERTO_SSH}"
  asegurar_linea_en_archivo "${archivo_config_ssh}" "PasswordAuthentication no"
  asegurar_linea_en_archivo "${archivo_config_ssh}" "PubkeyAuthentication yes"
  asegurar_linea_en_archivo "${archivo_config_ssh}" "PermitRootLogin no"
  asegurar_linea_en_archivo "${archivo_config_ssh}" "KbdInteractiveAuthentication no"

  cambios_despues="$(resumen_cambios)"

  if [[ "${cambios_despues}" -gt "${cambios_antes}" ]]; then
    info "La configuración de SSH cambió: se valida antes de tocar el servicio."

    # Si la sintaxis falla, corta sin tocar el servicio (estado seguro).
    if ! sshd -t; then
      error_fatal "La configuración nueva de sshd no pasa su propia validación (sshd -t). Se deja escrita en ${archivo_config_ssh}, pero no se reinicia el servicio: sshd sigue corriendo con la configuración anterior."
    fi

    # sshd -T lee la config EFECTIVA antes de reiniciar: si algo no ganó
    # precedencia, corta acá sin reiniciar.
    configuracion_efectiva="$(sshd -T 2>/dev/null || true)"

    # grep usa -i: sshd -T puede variar mayúsculas según el build de OpenSSH.
    # Port admite varios valores a la vez (sshd_config(5)): se compara el
    # conjunto, no solo el primero.
    puertos_efectivos="$(printf '%s\n' "${configuracion_efectiva}" | grep -iE '^port ' | awk '{print $2}' | sort -u)"

    if [[ "${puertos_efectivos}" != "${PUERTO_SSH}" ]]; then
      error_fatal "sshd -T reporta Port=[${puertos_efectivos:-<vacío>}] antes de reiniciar, se esperaba solo ${PUERTO_SSH}. Si aparece más de un valor, sshd va a seguir escuchando también en el puerto viejo (Port admite varias directivas a la vez). Dónde se declara Port hoy:
$(_ssh10_listar_declaraciones Port)
No se reinició sshd: el servicio sigue con la configuración anterior."
    fi

    # Estas cuatro son de un solo valor (sin "multiple options" en
    # sshd_config(5)): primer valor gana.
    directivas_a_verificar=(
      "PasswordAuthentication=no"
      "PermitRootLogin=no"
      "PubkeyAuthentication=yes"
      "KbdInteractiveAuthentication=no"
    )

    for entrada in "${directivas_a_verificar[@]}"; do
      directiva="${entrada%%=*}"
      esperado="${entrada#*=}"
      efectivo="$(printf '%s\n' "${configuracion_efectiva}" | grep -im1 "^${directiva} " | awk '{print $2}' || true)"
      if [[ "${efectivo}" != "${esperado}" ]]; then
        error_fatal "sshd -T reporta ${directiva}=${efectivo:-<vacío>} antes de reiniciar, se esperaba ${esperado}. Dónde se declara ${directiva} hoy:
$(_ssh10_listar_declaraciones "${directiva}")
No se reinició sshd: el servicio sigue con la configuración anterior."
      fi
    done

    info "sshd -T confirma la configuración efectiva antes de reiniciar: ${archivo_config_ssh} gana la precedencia esperada."

    if ! systemctl restart ssh; then
      error_fatal "No se pudo reiniciar el servicio ssh después de validar la configuración nueva."
    fi

    # sshd -T solo prueba que el archivo ganaría precedencia, no que el
    # proceso abrió el socket: se confirma con ss.
    if command -v ss &>/dev/null; then
      if ! ss -tln 2>/dev/null | grep -q ":${PUERTO_SSH} "; then
        error_fatal "sshd se reinició pero ss -tln no lo muestra escuchando en el puerto ${PUERTO_SSH}. Revisa el estado del servicio antes de cerrar esta sesión."
      fi
      info "Confirmado con ss -tln: sshd está escuchando en el puerto ${PUERTO_SSH}."
    else
      aviso "ss no está disponible: no se pudo confirmar con un socket real que sshd esté escuchando en el puerto ${PUERTO_SSH}. Verifícalo a mano antes de cerrar esta sesión."
    fi
  else
    info "La configuración de SSH ya estaba como se esperaba: no se reinicia sshd. Reiniciarlo en cada corrida rompería la regla de 0 cambios en la segunda pasada."
  fi

  asegurar_servicio_activo ssh

  aviso "IMPORTANTE: antes de cerrar esta sesión, verifica desde OTRA terminal que la conexión funciona por el puerto nuevo (ssh -p ${PUERTO_SSH} usuario@servidor). Solo después de confirmarlo, borra la regla del puerto 22 en el NSG con: az network nsg rule delete --nsg-name centinela-nsg -g centinela-rg -n permitir-ssh"
fi
