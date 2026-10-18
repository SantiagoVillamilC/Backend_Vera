# SCRUM-123 · IA-A2-02 · Catálogo cerrado de funciones del asistente

Implementa el registro, los esquemas, la validación y el despacho de HU-BE-IA-08.
El Anexo A de `Historias_de_Usuario_Asistente_Vera.docx` se actualizó localmente
con el estado de implementación. Esta tabla y los esquemas en
`internal/copilot/herramientas_esquemas.go` son su detalle versionado.

## Qué hace y cómo funciona

El modelo propone una llamada; `Catalogo.Ejecutar` comprueba que esté permitida,
valida los argumentos contra el mismo esquema enviado al proveedor, obtiene la
identidad del contexto autenticado y ejecuta el manejador conectado. Devuelve un
`ResultadoIA` con el mismo ID y nombre de la llamada, listo para reenviar al modelo.
No concatena resultados en las instrucciones del sistema.

El catálogo no permite registro dinámico de funciones adicionales. Hay seis
funciones de lectura y una de preparación; no hay clase de escritura. Agregar
una función exige cambiar código, pruebas y este anexo y revisar el cambio con
otro integrante, conforme a la HU.

## Anexo A: contrato y estado actual

| Función | Clase | Argumentos | Manejador real |
| --- | --- | --- | --- |
| `consultar_saldo` | lectura | `{}` | Conectado al núcleo en SCRUM-126 |
| `consultar_limites` | lectura | `{}` | Conectado al desglose del núcleo en SCRUM-126 |
| `listar_movimientos` | lectura | `desde`, `hasta`; opcionales `tipo`, `limite` | Conectado al núcleo en SCRUM-127 |
| `resumir_movimientos` | lectura | `desde`, `hasta`, `agrupar_por` | Conectado al núcleo en SCRUM-127 |
| `buscar_destinatario` | lectura | Exactamente uno: `celular` o `nombre` | Pendiente SCRUM-128 |
| `consultar_transferencia` | lectura | `transferencia_id` | Conectado al núcleo en esta HU |
| `preparar_transferencia` | preparación | `monto_pesos` y exactamente uno: `destinatario_ref`, `celular` o `llave_breb` | Pendiente SCRUM-131 |

Un manejador pendiente devuelve `FunctionUnavailable`, sin inventar saldo ni
crear acciones. Las siete declaraciones están disponibles mediante
`Catalogo.Declaraciones()`; la orquestación se implementará en SCRUM-124.
`cmd/api` construye el catálogo junto al proveedor e inyecta ambos mediante
`DependenciasVera`. Los endpoints del asistente siguen respondiendo 501 hasta
implementar sus historias, incluido el consentimiento.

### Restricciones de entrada

- Objeto JSON UTF-8, máximo 4096 bytes, sin claves duplicadas (incluidas claves
  escapadas equivalentes), sin campos adicionales, sin `null` ni contenido extra.
- Ningún esquema acepta usuario, parte, sesión, cuenta de origen ni conversación.
  `identidad.DeContexto` aporta la identidad al manejador. El futuro orquestador
  aportará la conversación desde estado del servidor, nunca desde argumentos IA.
- `desde` y `hasta`: fechas reales `YYYY-MM-DD`, ordenadas e inclusivas en hora de
  Colombia, máximo 366 días inclusivos. El núcleo convierte el rango a instantes para SQL.
- `tipo`: `deposito`, `transferencia_recibida`, `envio`, `gravamen`, `devolucion`
  o `devolucion_gravamen`; `limite`: entero de 1 a 20, predeterminado 20.
- `agrupar_por`: `motivo` o `contraparte`.
- `nombre`: de 2 a 100 caracteres, no sólo espacios. `celular`: de 7 a 24 caracteres
  de teléfono, normalizado y validado con `auth.NormalizarCelular` a E.164.
- `transferencia_id`: UUID canónico no nulo. El núcleo verifica titularidad; el
  adaptador la vuelve a comprobar y proyecta sólo estado, monto y fechas.
- `destinatario_ref`: 16–1024 caracteres de token (`A-Z`, `a-z`, `0-9`, `_`, `-`,
  `.`). SCRUM-128 verificará firma, vigencia y vinculación a usuario/conversación.
  `llave_breb`: 3–100 caracteres, no sólo espacios; su resolución es SCRUM-131.
- `monto_pesos`: string de dígitos, positivo, sin separadores, decimales, signo ni
  ceros iniciales. Máximo `92233720368547758`; se multiplica por 100 después de
  comprobar el rango `int64`. El manejador recibe además `Monto` como
  `nucleo.Centavos`. Los límites de negocio se validarán en SCRUM-131.

El validador es `kin-openapi v0.142.0`, que ya estaba en el grafo de dependencias;
ahora es dependencia directa, sin actualizar su versión. Sólo el límite de
registros usa el validador numérico. Los montos se validan como texto y se procesan
con enteros, sin conversiones por `float64`.

### Resultados y minimización

Los manejadores devuelven una de siete vistas internas (`DatosHerramienta`). El
catálogo comprueba que el tipo corresponda a la función y construye una proyección
explícita. No admite mapas arbitrarios, entidades del núcleo ni DTOs de auth.

- Importes: `{ "centavos": "12345", "pesos": "123.45 COP" }`, preservando el signo.
  El texto en pesos se deriva con `Centavos.PresentarUnidadesMayores`; el manejador
  no puede proporcionar una representación discordante.
- Nombres: iniciales enmascaradas (por ejemplo `L*** R***`), máximo dos componentes.
  Celulares: sólo los últimos tres dígitos. Es una proyección más restrictiva que
  la usada por auth y se limita a datos enviados al proveedor externo.
- Listas: máximo 20; movimientos respetan además el límite solicitado y entregan `total`.
  Los resúmenes muestran máximo 10 grupos, ordenados por entradas más salidas.
  Si se recorta una lista, `hay_mas=true`. Las listas vacías se serializan como `[]`.
- Resultado completo: máximo 16 KiB; si excede ese tamaño, se rechaza íntegramente.
- Fechas: RFC3339 con UTC-5. Los estados y motivos son valores conocidos.
- Una preparación sólo puede devolver estado `pendiente` y
  `requiere_confirmacion=true`, junto a la referencia de la acción, monto y
  destinatario enmascarado. No ejecuta ni reserva dinero.

Ejemplos de llamada y resultado del catálogo (internos; no son endpoints HTTP):

```json
{"nombre":"preparar_transferencia","argumentos":{"destinatario_ref":"referencia_opaca_123","monto_pesos":"50000"}}
```

El manejador recibe `Monto=5000000` centavos. Hasta conectar SCRUM-131, el resultado
es `{"error":{"codigo":"FunctionUnavailable"}}`.

```json
{"datos":{"disponible":{"centavos":"12345","pesos":"123.45 COP"},"retenido":{"centavos":"0","pesos":"0.00 COP"}}}
```

### Errores y trazabilidad

Errores de función como datos: `UnknownFunction`, `IdentityRequired`,
`ValidationError`, `FunctionUnavailable`, `NotFound`, `TooManyRequests` e
`InvalidFunctionResult`. Los errores internos libres nunca se devuelven al modelo.
La cancelación/plazo se propaga como error Go para detener al orquestador.

Cada intento de despacho registra con slog nombre conocido (o `desconocida`),
resultado, duración y **presencia** de argumentos validados, nunca sus valores.
El handler de logging agrega `request_id`. No se registran usuarios, sesiones,
montos, celulares, nombres, referencias, argumentos inválidos o mensajes de errores
internos. La tabla de auditoría persistente y defensa contra inyección de
instrucciones de SCRUM-138 siguen pendientes.

## Verificación

```sh
go test -race ./...
go vet ./...
go generate ./...
go test -race -cover ./internal/copilot
golangci-lint run ./internal/copilot/...
```

Pruebas: inventario cerrado y clases; copia inmutable de declaraciones; argumentos
válidos/inválidos para las siete funciones; identidad, duplicados y campos extra;
fechas y formatos; conversión y desbordamiento de montos; destino único; simulación
de errores; privacidad de logs y resultados; listas y tamaño; titularidad de
transferencias; ejecución concurrente y cancelación. No requieren proveedor IA,
credenciales ni una base de datos real para verificar el catálogo.
