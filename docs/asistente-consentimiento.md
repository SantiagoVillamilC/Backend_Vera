# Consentimiento del asistente — SCRUM-130

El asistente requiere un consentimiento opcional `asistente_ia` de la parte autenticada.
Se resuelve por `ParteID`, incluso cuando la sesión contiene otro `UsuarioID`.

## API

1. Consultar `GET /v1/auth/consents?tipo=asistente_ia`. Devuelve en `data`
   `tipo`, `version_texto`, `texto` y `autorizado`, aunque aún no existan aceptaciones.
   Sin filtro se conserva el historial de consentimientos del contrato anterior.
2. Presentar el texto y enviar `POST /v1/auth/consents`:
   `{"tipo":"asistente_ia","version_texto":"v1","aceptado":true,"accion":"otorgar","metodo":"app"}`.
3. Para revocar, consultar la versión actual y enviar el mismo cuerpo con
   `aceptado:false` y `accion:"revocar"`.

Cada evento agrega una fila inmutable con texto exacto, versión, actor, método y fecha.
Una versión ausente, futura o diferente de la vigente responde `422 ValidationError`
sin registrar un evento. El último evento insertado determina la autorización, siempre
que sea un otorgamiento de la versión vigente; la fecha no se usa como desempate.

## Control de acceso

Auth expone el puerto `ConsentimientoAsistenteVigente`; copilot no lee tablas de auth.
Los mensajes de texto y voz y la confirmación consultan este puerto sin caché.
También se protege cada llamada al proveedor, incluidas las futuras vueltas del orquestador.
Sin autorización se responde `403 ConsentRequired` y no se envía contenido al proveedor.
Una caída de la lectura bloquea el acceso con `503 AssistantUnavailable`.

Al confirmar sin consentimiento, primero se cancela la acción propia que siga pendiente.
Una acción ajena, inexistente o terminal no se modifica ni revela su existencia.
La revocación conserva conversaciones y evidencia; el borrado y la retención pertenecen
a sus historias correspondientes. Tampoco retira datos de una llamada ya iniciada.

La orquestación del chat y la ejecución financiera continúan pendientes de sus HUs:
con consentimiento válido esos endpoints mantienen su respuesta `501`.

## Versiones y revisión del texto

La migración auth 0013 agrega el tipo, un orden de eventos y el texto inicial `v1`.
Ese texto está identificado expresamente como **borrador pendiente de revisión del equipo
y asesor**; la implementación técnica no equivale a aprobación del texto legal.

Un cambio de proveedor, modalidad o condiciones requiere insertar otra versión en
`auth.textos_consentimiento` con su `vigente_desde`. Al entrar en vigencia, las aceptaciones
anteriores dejan de autorizar el uso. No se debe modificar el texto de una versión existente.
El rollback se rechaza si ya existen eventos IA, para conservar la evidencia.

## Validación

Las pruebas unitarias cubren autorización por llamada, revocación, identidad y fallos cerrados.
La prueba `TestVeraConsentimientoIntegracion`, con etiqueta `integration`, usa PostgreSQL
migrado y el rol `rol_api`: verifica el ciclo HTTP, cambio de versión, proveedor bloqueado,
preservación del historial y cancelación únicamente de la acción propia.
Requiere `DATABASE_URL` administrativo y `APP_DATABASE_URL` con el rol de la API.
