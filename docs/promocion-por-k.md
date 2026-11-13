# Promoción por K mínima (K=2)

La tesis declaraba la promoción por K como especificada y sin codificar. Esta es la versión mínima:
K=2 usuarios distintos que corrigen el mismo destino a la misma categoría promueven el destino en el
conocimiento compartido, y las filas **nuevas** de ese destino, de cualquier usuario, se enriquecen
con esa categoría. No hay recálculo retroactivo.

## Cómo se registra el voto

`CorregirMovimiento` (`internal/inteligencia/correccion.go`) escribe, en la misma transacción de la
corrección, un voto por cada clave del destino (`llave`, `mid`, `nombre normalizado`, las que existan).
La tabla `inteligencia.voto_destino` tiene clave única `(tipo_destino, nivel, valor, usuario_id)`:
si el usuario cambia de opinión, su voto se reemplaza, no se suma otro. Una corrección de nombre no
vota.

## La regla

- K=2 (`kVotosParaPromover`). Se cuentan **usuarios distintos** por categoría; el mismo usuario no
  suma dos veces.
- Empate a uno: dos categorías con un voto cada una no promueven nada.
- Si una categoría alcanza K, se escribe una fila en `inteligencia.promocion_destino` con la categoría
  y `promovido_en`. Si ya había una promoción, no se reescribe (`ON CONFLICT DO NOTHING`): la primera
  que alcanza K manda. Si dos categorías alcanzan K a la vez, gana la que tiene más votos y, en empate
  exacto, la primera alfabéticamente por la consulta (`ORDER BY votos DESC, categoria`).
- La promoción se suma a la memoria del usuario solo donde no haya una corrección privada
  (`memoriaDeUsuario` en `internal/inteligencia/enriquecimiento.go`): la memoria privada gana. Así un
  tercero recibe la categoría promovida en un movimiento nuevo sin que se le pise lo que corrigió a
  mano.
- Las filas ya enriquecidas no se recalculan: la promoción aplica al siguiente movimiento del destino.
  Es el límite declarado de la versión mínima.

## Privacidad

El voto y la promoción nunca se exponen en el contrato ni en la respuesta de corrección. La respuesta
sigue siendo `Movimiento` y `FilasCambiadas`; ninguna prueba inspecciona quién votó, y la tabla de
votos no tiene endpoint. El conocimiento compartido es una categoría por destino, no una lista de
personas.

## Pruebas

```bash
go test -count=1 -run TestCategoriaQueAlcanzaK ./internal/inteligencia/
set -a && source cargas/inteligencia/entorno.sh && set +a
go test -tags integration -count=1 -v -run TestDosUsuariosPromuevenYElTerceroHereda ./internal/inteligencia/
```

La unitaria cubre dos iguales, uno solo, empate a uno y la que tiene más. La de integración siembra
tres usuarios con el mismo comercio, corrige los dos primeros a `compras`, exige la promoción en
`promocion_destino`, siembra un pago nuevo del tercero y exige que se enriquezca con `compras` por
`memoria`. Un cuarto usuario corrige otro comercio una sola vez y se verifica que no promueve.

## Migración

`bd/migraciones/0035_create_voto_y_promocion.sql` crea las dos tablas con Down completo, las suma a
`sqlc.yaml` y las consultas viven en `bd/consultas-inteligencia/promocion.sql`. Los votos y la
promoción no tocan el ledger ni la memoria privada: `promocion_destino` es la única fuente del
conocimiento compartido y `memoria_correccion` sigue siendo por usuario.

## Lo que queda fuera

- Recalcular filas viejas cuando se promueve: prohibido por la decisión de la sesión.
- Revocar una promoción o cambiarla después: la versión mínima no la reescribe.
- Promoción por categoría visible distinta de la corregida: la corrección ya valida la categoría
  antes de votar (`EsCategoriaVisible`).
