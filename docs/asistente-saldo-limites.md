# Consulta de saldo y límites — SCRUM-126 / IA-A3-01

`cmd/api` conecta las funciones existentes `consultar_saldo` y `consultar_limites`
con manejadores de lectura del núcleo. Sus argumentos continúan siendo `{}`:
ninguna acepta cuenta, usuario, parte ni fecha proporcionados por el modelo.

La cuenta financiera se deriva de `UsuarioID`, igual que en los endpoints actuales.
`ParteID` sigue identificando al titular del consentimiento y conversación, pero no
reemplaza el identificador financiero del núcleo. Ambos proceden de la identidad
autenticada. El catálogo valida los argumentos antes de llamar al manejador.

## Saldo

`ConsultaSaldoIA` usa `Servicio.Saldo` sobre `NombreCuentaDisponible(UsuarioID)` y
`NombreCuentaRetenido(UsuarioID)`. Conserva el signo público del usuario y el mismo
tratamiento de moneda y ausencia de movimientos de `GET /v1/saldo`. No abre cuentas
ni crea movimientos; sin postings devuelve cero. Disponible y retenido son lecturas
sucesivas del ledger vivo, no una reserva ni una garantía de fondos para un envío futuro.

El resultado contiene `disponible`, `retenido` y `moneda:"COP"`. Los montos incluyen
`centavos` como cadena exacta y `pesos` como texto calculado con `Centavos`, sin floats.
El saldo retenido se presenta por separado y no se suma al disponible.

## Límites

`Servicio.DesgloseLimites` expone el cálculo que ya realiza el motor. El método público
`MontoMaximoEnviable` y las funciones privadas anteriores delegan en ese mismo cálculo,
de modo que el asistente y `GET /v1/transferencias/maximo` comparten reglas y redondeo.
El momento de consulta lo fija el servidor; se conserva el corte mensual en Colombia.

La proyección para IA contiene:

- `maximo_enviable`: mínimo entre el máximo por saldo/GMF, tope por operación y cupo restante.
- `saldo_despues_gmf`: máximo que permite el saldo considerando GMF, antes de los cupos.
- `tope_operacion`: tope vigente por operación, convertido desde la regla de UVT.
- `cupo_mensual_restante`: cupo vigente menos el acumulado que cuenta para el motor; mínimo cero.
- `gmf`: gravamen calculado para el **máximo final**, después de aplicar los cupos.
- `aplica_gmf`: verdadero si ese gravamen es positivo; con máximo cero es falso.
- `moneda`: COP.

`saldo_despues_gmf` no significa saldo menos el campo `gmf`: ese campo corresponde al
máximo final, que puede estar recortado por los cupos. Todos los importes usan la misma
proyección de centavos y pesos del catálogo. No se envían IDs de cuentas, datos KYC ni
entidades del ledger al proveedor.

## Errores y validación

Sin configuración vigente de UVT, límites, GMF o lector KYC, el endpoint del máximo
responde `503 LimitsUnavailable` sin detalle interno. El catálogo comunica errores de
lectura como `FunctionUnavailable`, para que el orquestador explique la indisponibilidad
sin inventar valores. Cancelación y plazos se propagan y detienen el ciclo.

Las pruebas unitarias cubren desglose, saldo cero, GMF, cupo agotado, errores de reglas,
identidad, fechas del servidor y rechazo de argumentos para otra persona. Las pruebas
de integración comparan catálogo y endpoints reales, con distintos saldos de terceros,
y verifican mediante una huella de cuentas, asientos y postings que no hubo escrituras.
Requieren `DATABASE_URL` administrativo y `APP_DATABASE_URL` con `rol_api`, bajo el tag
`integration`, sobre una base desechable. No hay migraciones nuevas para esta HU.
