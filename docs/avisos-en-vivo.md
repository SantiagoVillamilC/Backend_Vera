# Avisos en vivo: cuando se emiten y como se comprueba

El aviso de consumo llega a la bandeja por tres caminos, todos apagados por defecto y todos bajo la
misma bandera. Este documento dice cuando corre cada uno, que frenos tiene, que latencia se puede
esperar y que pasa cuando al usuario le falta historial. El canal de notificacion sigue siendo la
bandeja de avisos: no hay envio externo (`aviso_entrega` existe en el modelo y no tiene
implementacion de envio).

## Los tres caminos

| Camino | Cuando | Lo arranca |
| --- | --- | --- |
| Confirmacion de liquidacion | Al asentar el gasto, en la misma sesion del usuario | `nucleo.ConAvisoTrasAsiento`, conectado en `cmd/api/main.go` |
| Arranque de la API | Una vez, enseguida tras levantar los jobs | `lanzarAvisosAlArrancar` en `cmd/api/wiring_avisos.go` |
| Job diario | Todos los dias a `AVISOS_HORA` (06:00 de Colombia) | `AvisosWorker` en `internal/platform/jobs/jobs.go` |

Los tres pasan por la misma emision (`internal/inteligencia/avisos_servicio.go`): candado de
usuario, deduplicacion por clave y periodo, cupo mensual. Nada se emite si `AVISOS_ACTIVO=false`,
que es el valor de `produccion` y de `local` (`infra/servidor/banderas.tsv`). `demo` es la unica
superficie encendida. La corrida al arrancar esta descrita en `docs/operacion-demo.md` y la
proyeccion que sostiene el aviso de presupuesto, en `docs/proyeccion-presupuestal.md`.

## Confirmacion de liquidacion

Los dos caminos que confirman una liquidacion llaman la opcion despues del commit, nunca adentro:

- `internal/nucleo/outbox.go` (`cerrarConfirmada`) tras cerrar el despacho confirmado.
- `internal/nucleo/conciliacion.go` (`conciliarVentana`) cuando el extracto del riel confirma la
  operacion y la corrida commit entera termino.

El callback llega con el usuario titular de la cuenta (`transferencia.usuario_id`) y con la hora del
servicio. Un rechazo, una compensacion o un cierre que no gano la transicion no lo disparan. El
nucleo no importa inteligencia: `cmd/api` inyecta la emision con `nucleo.ConAvisoTrasAsiento`, y el
paquete de inteligencia expone `EmitirAvisosDelUsuario`, que envuelve la misma emision de siempre.
Una falla o un panico del callback se registran y la confirmacion termina igual.

## El freno, por que existe y que cuesta

Confirmar un pago dispara una emision por usuario. Para que un usuario que confirma en rafaga no
multiplique consultas al ledger, `frenoDeEmisiones` (`cmd/api/wiring_avisos.go`) permite como maximo
una emision por minuto y usuario. El mapa es en memoria y se poda solo cuando pasa el intervalo, asi
que no crece sin fin.

La emision sale en una goroutine con `context.WithoutCancel`: que la peticion HTTP termine no la
corta. Latencia esperada desde que la confirmacion responde hasta que el aviso aparece en la
bandeja: segundos (una lectura de contexto y la insercion), no minutos. Un segundo aviso del mismo
usuario dentro del mismo minuto simplemente se salta: el lote de las 06:00 o la proxima confirmacion
lo cubren, y la deduplicacion por clave y periodo hace que nunca falte ni se duplique.

## Cuando el historial no alcanza

La deteccion de pagos necesita al menos `MesesMinimos` (3) meses completos con actividad para
comparar. Con menos, `pago_no_alcanza` responde con `historial_insuficiente` y no se emite tarjeta.
Con historial pero sin series maduras en la ventana de siete dias, responde `sin_series` o
`sin_pago_en_la_ventana`. Son las causas que `internal/inteligencia/avisos/detectores.go` documenta
en la decision, no un error del camino en vivo.

## Como comprobarlo

Las pruebas de la fase, contra la base desechable (`infra/local/levantar-base.sh`):

```bash
set -a && source cargas/inteligencia/entorno.sh && set +a
go test -tags integration -count=1 -v -run 'TestAvisoTrasAsiento' ./internal/nucleo/
go test -tags integration -count=1 -v -run 'TestElAvisoDePagoLlegaSinEsperarElLote' ./internal/inteligencia/
go test -count=1 ./cmd/api/
```

La primera demuestra que el callback corre despues del commit y solo en confirmaciones, con el
titular. La segunda siembra tres meses de pagos recurrentes, deja el saldo por debajo de la suma
esperada y exige `pago_no_alcanza` sin correr el lote. La tercera cubre el freno y el arranque.
