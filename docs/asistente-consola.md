# Probar Vera desde una consola

Con Docker Desktop abierto, haz doble clic en **`Probar-Vera.cmd`**, en la raíz
del backend. Escribe tu pregunta cuando aparezca `Tú >` y pulsa Enter.
El arranque inicial puede tardar mientras Go compila el programa.

El archivo carga la clave y el modelo desde `.env.local`, que ya tienes
configurado y está excluido de Git. La consola usa Gemini real con herramientas
de prueba: saldo, movimientos y destinatarios son **ficticios**. No consulta
la base de datos ni puede ejecutar transferencias. Usa datos inventados en tus
preguntas; los argumentos y resultados se muestran en la terminal.

## Qué aparece en pantalla

```text
Tú > ¿Cuánto tengo?

[ FUNCIÓN ELEGIDA | argumentos ] "consultar_saldo"
{}

[ BACKEND DE PRUEBA | resultado ] "consultar_saldo"
{ "datos": { "disponible": { "centavos": "12000000", ... } } }

[ VERA | respuesta final ]
Tu saldo disponible es 120000.00 COP.

[ RESUMEN ] 1 función(es) | 2 vuelta(s) | ... s
Solicitudes de la sesión: 2/20
```

Se muestran las llamadas que pidió el modelo y los resultados que **realmente
produjo el catálogo** tras validar argumentos y enmascarar datos. Si la pregunta
requiere una aclaración, puede no aparecer ninguna función. Si prepara una
propuesta, también se muestra la tarjeta simulada; no hay un comando para
confirmar o mover dinero. La respuesta final se imprime después de pasar por
el orquestador, no directamente desde una respuesta cruda del proveedor.

## Preguntas y comandos

- `¿Cuánto tengo?`
- `Lista mis movimientos de ayer`
- `Resume este mes por motivo`
- `Busca a Laura y prepara 50 mil`
- `Prepara 50 mil a la llave 1234567890` (debería preguntar el tipo).
- `Envía cincuenta a Laura` (debería aclarar el monto).
- `/ayuda`: ejemplos.
- `/nuevo`: borra el contexto; conserva el consumo de solicitudes.
- `/cuota`: muestra las llamadas consumidas durante esta sesión.
- `/salir`: cierra el programa.

El contexto se conserva entre preguntas mientras la consola esté abierta. Los
datos de prueba son estáticos; sus fechas se adaptan al rango consultado, pero
no representan una cuenta real. Preparar una propuesta no cambia el saldo.
El programa no crea archivos con el historial. Si falla un turno, descarta su
contexto parcial, conservando las preguntas anteriores y la cuota consumida.

## Demo sin consumir cuota

Desde PowerShell, situado en la raíz del backend:

```powershell
.\Probar-Vera.cmd -demo
```

Esta demo ejecuta un ejemplo fijo con proveedor simulado, sin clave ni llamadas
a Google. Usa el mismo catálogo y orquestador. Puedes ver su salida guardada en
`evaluaciones/consola-demo.txt`; no es evidencia de comprensión de Gemini.

## Límite y ejecución manual

El lanzador abre una sesión de **máximo 20 solicitudes**, con pausa de 12
segundos entre ellas y sin reintentos. Una pregunta suele necesitar varias
llamadas. `/nuevo` no restablece el límite. Un rechazo de credenciales o cuota
de Google detiene la sesión. No activa facturación ni cambia de modelo.

Para ajustar explícitamente el límite al iniciar:

```powershell
.\Probar-Vera.cmd -max-solicitudes 10
```

Si tienes Go 1.27 y las variables ya cargadas en tu entorno:

```powershell
go run ./cmd/consola-vera -nivel-gratuito-confirmado
go run ./cmd/consola-vera -demo
```

El binario no carga archivos dotenv por sí mismo: el lanzador los carga con
Docker. `-pregunta "¿Cuánto tengo?"` permite ejecutar una sola pregunta y salir.
La consola no ejecuta el conjunto completo de evaluación. Las pruebas locales
usan proveedores escritos a mano y la demo; no consumen cuota.
