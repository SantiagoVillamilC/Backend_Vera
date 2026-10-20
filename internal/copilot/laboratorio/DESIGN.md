---
name: Vera · Laboratorio de voz
description: Herramienta local para conversar y observar las funciones del asistente con datos ficticios.
colors:
  fondo: "#07080f"
  superficie: "#0f1020"
  elevado: "#161829"
  borde: "#30344f"
  texto: "#f2f3ff"
  secundario: "#b1b5cf"
  violeta: "#6c4fd9"
  cian: "#00d4ff"
  verde: "#00e5a0"
  error: "#ff91a8"
typography:
  body:
    fontFamily: 'Roboto, "Segoe UI", sans-serif'
    fontSize: "16px"
  headline:
    fontSize: "36px"
    fontWeight: 600
    lineHeight: 1.16
  code:
    fontFamily: 'Consolas, "Courier New", monospace'
    fontSize: "12px"
    lineHeight: 1.6
rounded:
  control: "9px"
  panel: "14px"
spacing:
  panel-gap: "28px"
  desktop-inset: "38px"
---

# Design System: Vera · Laboratorio de voz

## Overview

Superficie de operación local: preguntar por texto o micrófono, leer o escuchar la respuesta y examinar argumentos y resultados de herramientas. La conversación es el foco; la traza explica el último turno sin mezclar JSON con el diálogo. Los datos financieros y las propuestas son ficticios.

El código de referencia es `web/index.html`, `web/estilos.css` y `web/app.js`. La identidad parte de `../app/lib/core/theme/app_theme.dart`, relativa a la raíz del backend: fondos oscuros y acentos violeta, cian y verde. El laboratorio usa texto secundario y bordes más claros que ese tema. No carga fuentes externas: utiliza las fuentes disponibles del sistema.

## Colors

El violeta distingue Enviar; el cian identifica a Vera, la transcripción y el foco de teclado. El verde señala consumo y resultados del backend. El rosa muestra errores y grabación activa, acompañado siempre de texto. La distinción entre fondo, panel y controles depende de tonos y bordes, sin gradientes decorativos.

## Typography

La pila de cuerpo conserva el carácter sans serif del tema de la app. El título principal usa espaciado compacto; mensajes y explicaciones mantienen interlineado abierto. Las trazas usan monoespaciada y ajuste de líneas, incluidos argumentos largos. El contador de cuota usa cifras tabulares. Las etiquetas de autor son pequeñas y separadas del contenido.

## Layout

Cabecera y contenido comparten un máximo de 1380 px. Dos columnas de proporción 1,25:1 separan conversación y traza. La conversación contiene bienvenida o mensajes desplazables, estado, transcripción, reproducción del clip, formulario, grabación y lectura en voz alta. La traza conserva modelo y versión de instrucciones en su pie.

Por debajo de 850 px, el CSS apila las columnas, reduce márgenes y elimina el límite de altura de la traza. Esto describe la regla implementada; no acredita una validación móvil. En escritorio, mensajes y eventos tienen desplazamiento independiente.

## Elevation & Depth

Superficies planas, sin sombras. Bordes finos y cambios de tono separan diálogo, entrada, eventos y controles. El estado de interacción se muestra con color de fondo, borde y texto; las transiciones son breves y se desactivan con `prefers-reduced-motion`.

## Shapes

Panel principal suavemente redondeado; botones y tarjeta de propuesta comparten una curva menor. Los bloques JSON tienen fondo propio y esquinas contenidas. No se usa una burbuja distinta para cada mensaje: autor, sangría y divisores dan jerarquía al historial.

## Components

- **Sesión:** modo demo o Gemini, consumo acumulado y límite. Nueva conversación borra el diálogo, pero conserva el consumo. Al agotarse la cuota se deshabilita la entrada; no hay reintento automático.
- **Texto:** etiqueta visible, máximo de 2000 caracteres; Enter envía y Shift+Enter introduce una línea. Durante consultas los controles de entrada quedan deshabilitados.
- **Grabación:** permiso del navegador → grabación con contador y botón «Detener y enviar» → transcripción → consulta → respuesta. Se detiene a los 20 segundos y envía automáticamente. El clip queda reproducible y la transcripción visible. Los errores permiten volver a escribir o grabar, salvo un bloqueo de cuota o credenciales.
- **Lectura:** síntesis de voz del navegador, selector de voz, botón para escuchar y detener, y lectura automática opcional desmarcada inicialmente. Prioriza voces españolas cuando existen. La disponibilidad depende del navegador y del sistema; no es audio generado por Gemini.
- **Traza:** detalles desplegables, abiertos al recibirlos, para función y argumentos o resultado del backend. Se reemplaza en cada turno; no es una transmisión de eventos en vivo. La propuesta de transferencia informa que es simulada y no ejecuta dinero.
- **Accesibilidad:** documento en español, formulario etiquetado, log y estado con anuncios corteses, errores con `role="alert"`, foco cian visible y controles nativos de audio y desplegables. El estado no depende sólo del color. Son mecanismos presentes en el código, sin afirmar auditoría con lector de pantalla.

## Do's and Don'ts

- Mantener visibles datos ficticios, modo, cuota y procedencia de la voz.
- Conservar la pregunta por texto como alternativa al micrófono y los errores recuperables junto a la entrada.
- No presentar el modo demo como transcripción real: devuelve una pregunta y respuesta fijas sin Google, aunque permite probar la captura local.
- No afirmar validación de micrófono físico, calidad de transcripción real ni compatibilidad móvil a partir de la revisión de escritorio.
- Mantener el alcance local: audio e historial permanecen en memoria de esta herramienta; en modo Gemini el audio se envía al proveedor para transcribir. La clave permanece en el servidor y nunca forma parte de la interfaz.
