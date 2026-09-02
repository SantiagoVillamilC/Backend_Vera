#!/usr/bin/env bash
# infra/servidor/pasos/10-ssh.sh
#
# Endurece el acceso por SSH (tarjeta SCRUM-28): mueve el puerto al fijado
# en infra/servidor/red-fijada.sh y deja explícita la autenticación
# exclusivamente por llave pública, sin depender de lo que ya haya puesto
# cloud-init.
#
# Este archivo se carga con source desde aprovisionar.sh, no se ejecuta como
# proceso aparte. Por eso el cuerpo entero va dentro de bloques if/else en
# lugar de usar "return" para salir temprano: un "return" en un script
# sourceado desde dentro de una función (ejecutar_paso, en aprovisionar.sh)
# no solo termina este archivo, termina esa función, saltándose el conteo
# de _APROV_TOTAL_PASOS (ver la nota de cabecera de 00-verificar-sistema.sh,
# que documenta la misma trampa). La única función que sí se define acá
# (_ssh10_listar_declaraciones, más abajo) puede usar "return" con
# normalidad: ese "return" sale de la función, no del archivo.

NOMBRE_PASO="Endurecer el acceso por SSH"
TARJETA="SCRUM-28"

paso "${NOMBRE_PASO} (${TARJETA})"

# Ayuda de diagnóstico para cuando una directiva no coincide con lo
# esperado: recorre /etc/ssh/sshd_config.d/*.conf en orden alfabético (el
# mismo orden en que sshd los incluye, ver más abajo) y por último
# /etc/ssh/sshd_config, e imprime cada archivo que declara la directiva
# junto con la línea exacta. Es una búsqueda de texto, no el parser real de
# sshd (no entiende bloques Match ni continuaciones de línea): sirve para
# que un mensaje de error nombre un archivo concreto en vez de decir "algo
# no cuadró", no reemplaza la comparación contra sshd -T de más abajo, que
# es la que de verdad decide si el paso falla.
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

if ! command -v systemctl &>/dev/null; then
  # El contenedor de prueba de infra/servidor/probar-en-docker.sh no tiene
  # systemd: ni siquiera existe el binario systemctl. Sin él no hay forma de
  # comprobar el estado de ssh.socket ni de reiniciar sshd, así que este
  # paso completo se omite acá y se deja su verificación real para la VM
  # (Azure o Vagrant), igual que ya hace asegurar_servicio_activo en
  # lib/comun.sh.
  aviso "systemctl no está disponible en este sistema: se omite por completo el endurecimiento de SSH. Verifícalo en la VM real (Azure o Vagrant)."
else
  # ssh.socket habilitado es un caso fatal, no uno que se pueda avisar y
  # seguir: con activación por socket, la directiva "Port" de sshd_config se
  # ignora en silencio (systemd, no sshd, es quien escucha el puerto), así
  # que el servidor seguiría aceptando conexiones en el 22 mientras este
  # paso escribe su configuración y reporta éxito. Hoy la imagen fijada trae
  # ssh.socket deshabilitado, pero una actualización de la imagen podría
  # cambiarlo sin que nadie se entere si este chequeo no estuviera acá.
  if systemctl is-enabled ssh.socket &>/dev/null; then
    error_fatal "ssh.socket está habilitado. Con activación por socket la directiva Port de sshd_config se ignora en silencio y el servidor seguiría escuchando en el puerto 22. Deshabilítalo primero con: systemctl disable --now ssh.socket"
  fi
  info "ssh.socket no está habilitado, como se esperaba: la directiva Port de sshd_config sí va a tener efecto."

  # Por qué 10- y no 60-: sshd_config incluye sshd_config.d/*.conf al
  # principio del archivo (línea 2 en /etc/ssh/sshd_config) y los procesa en
  # orden alfabético del glob. sshd_config(5) es explícito: "Unless noted
  # otherwise, for each keyword, the first obtained value will be used"
  # (gana el PRIMER valor, no el último; verificado en esta máquina con
  # zcat /usr/share/man/man5/sshd_config.5.gz). Un archivo con número mayor
  # que 50-cloud-init.conf habría quedado sin efecto para toda directiva
  # que cloud-init ya declare: exactamente el modo de falla en silencio que
  # hay que evitar. La intuición natural (como en runlevels o en los sitios
  # de nginx) es que un número mayor gana; acá es al revés.
  #
  # Pero el número por sí solo es solo una apuesta razonable, no una
  # garantía: esta imagen usa "50-", pero otra versión de cloud-init podría
  # usar "05-" o "00-" y volveríamos a perder la precedencia en silencio sin
  # que nada lo avise. Por eso el paso no se queda solo con el nombre del
  # archivo: más abajo relee la configuración EFECTIVA con "sshd -T" y
  # compara contra lo esperado antes de reiniciar. Esa comparación, no el
  # número, es la garantía real.
  #
  # Alternativa descartada a propósito: insertar las directivas arriba de
  # la línea "Include" de /etc/ssh/sshd_config (línea 2) ganaría siempre sin
  # depender de ningún número, pero ese archivo lo administra el paquete
  # openssh-server, y editarlo produce un conflicto de conffile en cada
  # actualización del paquete (dpkg pregunta qué hacer con el archivo
  # modificado, o lo sobrescribe). Se prefirió la detección explícita de
  # más abajo sobre editar un archivo que no es nuestro.
  archivo_config_ssh="/etc/ssh/sshd_config.d/10-centinela.conf"

  asegurar_directorio "$(dirname "${archivo_config_ssh}")"

  cambios_antes="$(resumen_cambios)"

  asegurar_linea_en_archivo "${archivo_config_ssh}" "Port ${PUERTO_SSH}"
  # Se fija explícitamente aunque cloud-init ya lo haya puesto en
  # 50-cloud-init.conf (ver infra/servidor/README.md o el estado medido del
  # servidor real): ese archivo puede cambiar con una actualización de la
  # imagen o editarse a mano, y entonces el servidor quedaría aceptando
  # contraseñas sin que nadie lo note. El aprovisionamiento deja el estado
  # explícito y verificable acá, no heredado de otro archivo.
  asegurar_linea_en_archivo "${archivo_config_ssh}" "PasswordAuthentication no"
  asegurar_linea_en_archivo "${archivo_config_ssh}" "PubkeyAuthentication yes"
  asegurar_linea_en_archivo "${archivo_config_ssh}" "PermitRootLogin no"
  asegurar_linea_en_archivo "${archivo_config_ssh}" "KbdInteractiveAuthentication no"

  cambios_despues="$(resumen_cambios)"

  if [[ "${cambios_despues}" -gt "${cambios_antes}" ]]; then
    info "La configuración de SSH cambió: se valida antes de tocar el servicio."

    # Se corta acá si la sintaxis no es válida, sin tocar el servicio: el
    # archivo queda escrito, pero sshd sigue corriendo con la configuración
    # anterior, que es el estado seguro.
    if ! sshd -t; then
      error_fatal "La configuración nueva de sshd no pasa su propia validación (sshd -t). Se deja escrita en ${archivo_config_ssh}, pero no se reinicia el servicio: sshd sigue corriendo con la configuración anterior."
    fi

    # "sshd -T" lee la configuración EFECTIVA desde disco sin tocar el
    # servicio en ejecución: por eso esta comprobación corre ANTES de
    # reiniciar, no después. Si algo no ganó la precedencia esperada,
    # error_fatal corta acá, sin reiniciar: sshd sigue corriendo con la
    # configuración anterior (el estado seguro) y no nos quedamos sin
    # acceso al servidor.
    configuracion_efectiva="$(sshd -T 2>/dev/null || true)"

    # Las claves de "sshd -T" se comparan sin distinguir mayúsculas: se
    # probó en dos builds distintos de OpenSSH (Arch, OpenSSH_10.5p1, las
    # imprime en CamelCase: "Port", "PasswordAuthentication"; Debian 13,
    # OpenSSH_10.0p2-Debian, que es el sistema fijado de verdad, las
    # imprime en minúscula: "port", "passwordauthentication"). El "-i" en
    # cada grep de más abajo hace que esta comparación no dependa de cuál
    # de los dos formatos use la versión de OpenSSH que termine corriendo.
    #
    # Port es distinto a las otras cuatro directivas: sshd_config(5) dice
    # explícitamente para ella "Multiple options of this type are
    # permitted" (confirmado en el man page de Debian 13 y con una prueba
    # local: dos directivas Port en dos archivos hacen que sshd escuche en
    # AMBOS puertos a la vez, no que gane la primera). Por eso no basta con
    # mirar el primer valor de Port: hay que confirmar que el ÚNICO puerto
    # efectivo es PUERTO_SSH. Si otro archivo también declarara Port, sshd
    # seguiría escuchando en el puerto viejo además del nuevo, sin que una
    # comparación de "primer valor" lo note.
    puertos_efectivos="$(printf '%s\n' "${configuracion_efectiva}" | grep -iE '^port ' | awk '{print $2}' | sort -u)"

    if [[ "${puertos_efectivos}" != "${PUERTO_SSH}" ]]; then
      error_fatal "sshd -T reporta Port=[${puertos_efectivos:-<vacío>}] antes de reiniciar, se esperaba solo ${PUERTO_SSH}. Si aparece más de un valor, sshd va a seguir escuchando también en el puerto viejo (Port admite varias directivas a la vez). Dónde se declara Port hoy:
$(_ssh10_listar_declaraciones Port)
No se reinició sshd: el servicio sigue con la configuración anterior."
    fi

    # Las cuatro de acá abajo sí son de un solo valor (ninguna trae la nota
    # "multiple options" en sshd_config(5)), así que "primer valor
    # obtenido" es la comparación correcta.
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

    # Confirmación final, después de reiniciar: que el proceso en ejecución
    # de verdad quedó escuchando en el puerto nuevo. La comparación de
    # arriba contra sshd -T solo demuestra que el ARCHIVO ganaría esa
    # precedencia; no prueba que el proceso reiniciado haya podido abrir
    # ese socket (podría fallar el bind por otra razón, por ejemplo el
    # puerto ya ocupado por otro proceso).
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
