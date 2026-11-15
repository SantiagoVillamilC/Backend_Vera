#!/usr/bin/env bash
# infra/local/lib-demo.sh
# Piezas compartidas por demo-inyectar.sh y demo-limpiar.sh. Se carga con source, no se ejecuta.
#
# Requiere que el guion que la carga defina antes la función error (imprime y sale con código 1) y
# las variables DATABASE_URL y DEMO_USUARIO. Aporta:
#   - La lista de usuarios protegidos y el usuario de demo por defecto.
#   - preparar_conexion: valida el DSN y lo parte en PG_USUARIO, PG_CLAVE, PG_BASE y PG_SSLMODE.
#   - psql_demo: único punto de entrada a psql. El DSN no viaja como argumento, así que la clave no
#     aparece en la lista de procesos. Los datos de conexión van por variables de entorno que solo
#     ve el proceso de psql, con host y puerto fijos del túnel.
#   - rechazar_usuario_protegido y exigir_usuario_de_demo: las defensas sobre DEMO_USUARIO.

# Usuarios reales de la demo que jamás se escriben ni se borran. Camila y Valentina Rios.
PROTEGIDOS=(
  "8a665137-c89b-5afe-974a-91ad20f974f4"
  "8407bb2a-cab9-4d42-a111-3a7b64466571"
)
USUARIO_POR_DEFECTO="11111111-1111-4111-8111-111111111111"

# Host y puerto del túnel SSH. Son fijos: el DSN solo puede apuntar aquí.
PG_HOST="127.0.0.1"
PG_PUERTO="55437"

PG_USUARIO=""
PG_CLAVE=""
PG_BASE=""
PG_SSLMODE="prefer"

# Decodifica los %XX de un componente de URL, como hace libpq con un DSN.
decodificar_url() {
  local resto="$1" salida="" byte
  while [[ "$resto" =~ ^([^%]*)%([0-9A-Fa-f]{2})(.*)$ ]]; do
    salida+="${BASH_REMATCH[1]}"
    printf -v byte '%b' "\\x${BASH_REMATCH[2]}"
    salida+="$byte"
    resto="${BASH_REMATCH[3]}"
  done
  printf '%s' "$salida$resto"
}

# Valida DATABASE_URL con una expresión estricta y extrae usuario, clave, base y sslmode.
preparar_conexion() {
  local dsn_valido='^postgres(ql)?://([^@/]+)@127\.0\.0\.1:55437/(vera_semilla_[A-Za-z0-9_]+)(\?sslmode=([a-z-]+))?$'
  [[ -n "$DATABASE_URL" ]] || error "exporta DATABASE_URL del dueño por el túnel"
  [[ "$DATABASE_URL" =~ $dsn_valido ]] ||
    error "DATABASE_URL debe apuntar a 127.0.0.1:55437/vera_semilla_ (túnel SSH) y solo admite sslmode"
  local credenciales="${BASH_REMATCH[2]}"
  PG_BASE="${BASH_REMATCH[3]}"
  PG_SSLMODE="${BASH_REMATCH[5]:-prefer}"
  PG_USUARIO="$(decodificar_url "${credenciales%%:*}")"
  PG_CLAVE=""
  if [[ "$credenciales" == *:* ]]; then
    PG_CLAVE="$(decodificar_url "${credenciales#*:}")"
  fi
  [[ -n "$PG_USUARIO" ]] || error "DATABASE_URL debe incluir el usuario del dueño"
}

# Un solo punto de entrada a psql: sin ~/.psqlrc, parando en el primer error y con el usuario como
# variable para que ninguna consulta lo concatene.
psql_demo() {
  PGUSER="$PG_USUARIO" PGPASSWORD="$PG_CLAVE" PGDATABASE="$PG_BASE" PGSSLMODE="$PG_SSLMODE" \
    psql -h "$PG_HOST" -p "$PG_PUERTO" -X -v ON_ERROR_STOP=1 -v u="$DEMO_USUARIO" "$@"
}

# Falla sin tocar la base si DEMO_USUARIO es uno de los usuarios protegidos.
rechazar_usuario_protegido() {
  local protegido
  for protegido in "${PROTEGIDOS[@]}"; do
    [[ "$DEMO_USUARIO" != "$protegido" ]] || error "el usuario $protegido está protegido y nunca se borra"
  done
}

# Solo es válido el usuario de demo por defecto o uno con fila en public.demo_escenario.
exigir_usuario_de_demo() {
  [[ "$DEMO_USUARIO" != "$USUARIO_POR_DEFECTO" ]] || return 0
  local en_manifiesto
  en_manifiesto="$(psql_demo -At <<<"SELECT count(*) FROM public.demo_escenario WHERE usuario_id = :'u'::uuid;")" ||
    error "no se pudo consultar public.demo_escenario (¿túnel caído, clave incorrecta o migraciones sin aplicar?)"
  [[ "$en_manifiesto" == "1" ]] ||
    error "el usuario $DEMO_USUARIO no es el de demo por defecto ni tiene fila en public.demo_escenario: no se borra"
}

# Variante para demo-inyectar.sh. Además de los usuarios válidos para limpiar, admite un usuario
# nuevo, que no existe en auth.partes, auth.credenciales ni nucleo.cuenta: es el caso de una cuenta
# de demo adicional que todavía no se sembró. Un usuario que ya existe y no tiene manifiesto es una
# cuenta real y se rechaza, porque login-demo sobrescribiría su celular y su contraseña.
exigir_usuario_de_demo_o_nuevo() {
  [[ "$DEMO_USUARIO" != "$USUARIO_POR_DEFECTO" ]] || return 0
  local situacion
  situacion="$(psql_demo -At -F ' ' <<<"SELECT (SELECT count(*) FROM public.demo_escenario WHERE usuario_id = :'u'::uuid),
       (SELECT count(*) FROM auth.partes WHERE id = :'u'::uuid)
     + (SELECT count(*) FROM auth.credenciales WHERE parte_id = :'u'::uuid)
     + (SELECT count(*) FROM nucleo.cuenta WHERE titular_id = :'u');")" ||
    error "no se pudo consultar el estado del usuario (¿túnel caído, clave incorrecta o migraciones sin aplicar?)"
  local en_manifiesto existentes
  read -r en_manifiesto existentes <<<"$situacion"
  [[ "$en_manifiesto" == "1" || "$existentes" == "0" ]] ||
    error "el usuario $DEMO_USUARIO no es el de demo por defecto ni tiene fila en public.demo_escenario: no se escribe sobre una cuenta que no es de demo"
}
