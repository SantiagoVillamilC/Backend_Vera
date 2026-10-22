---
name: Vera · Laboratorio de voz
description: Conversación clara con texto y voz, y trazas verificables con datos ficticios.
colors:
  fondo: "#eef3f8"
  superficie: "#ffffff"
  elevado: "#f4f8fb"
  borde: "#dce5ee"
  texto: "#16222e"
  secundario: "#4f6170"
  terciario: "#5f7383"
  azul: "#2b7bc4"
  azul-texto: "#1f6fb2"
  azul-suave: "#e6f2fb"
  agua: "#177a6c"
  agua-suave: "#ddf3ef"
  error: "#9e2f4c"
  error-suave: "#fdecef"
typography:
  display:
    fontFamily: '"Playfair Display", Georgia, serif'
    fontSize: "35px"
    fontWeight: 700
    lineHeight: 1
    letterSpacing: "-0.02em"
  headline:
    fontFamily: '"Plus Jakarta Sans", "Segoe UI", sans-serif'
    fontSize: "38px"
    fontWeight: 600
    lineHeight: 1.2
    letterSpacing: "-0.035em"
  body:
    fontFamily: '"Plus Jakarta Sans", "Segoe UI", sans-serif'
    fontSize: "16px"
  message:
    fontFamily: '"Plus Jakarta Sans", "Segoe UI", sans-serif'
    fontSize: "13px"
    lineHeight: 1.8
  label:
    fontSize: "11px"
    fontWeight: 600
  code:
    fontFamily: '"JetBrains Mono", Consolas, monospace'
    fontSize: "12px"
    fontWeight: 500
    lineHeight: 1.8
rounded:
  control: "13px"
  panel: "24px"
  panel-mobile: "20px"
  code: "11px"
  pill: "100px"
spacing:
  panel-gap: "24px"
  desktop-inset: "40px"
  mobile-inset: "16px"
components:
  button-primary:
    backgroundColor: "{colors.texto}"
    textColor: "{colors.superficie}"
    rounded: "{rounded.control}"
    padding: "11px 16px"
  button-secondary:
    backgroundColor: "rgba(255,255,255,.8)"
    textColor: "{colors.texto}"
    rounded: "{rounded.control}"
    padding: "11px 17px"
  input-question:
    backgroundColor: "{colors.superficie}"
    textColor: "{colors.texto}"
    rounded: "{rounded.control}"
    padding: "13px 14px"
  chip-example:
    backgroundColor: "{colors.azul-suave}"
    textColor: "{colors.azul-texto}"
    rounded: "{rounded.pill}"
    padding: "9px 14px"
  conversation-panel:
    backgroundColor: "{colors.superficie}"
    textColor: "{colors.texto}"
    rounded: "{rounded.panel}"
---

# Design System: Vera · Laboratorio de voz

## Overview

**Creative North Star: "Claridad de conversación"**

El laboratorio comparte la identidad clara de Mockups y Landing: perla frío, blanco, tinta profunda y acentos cielo y agua. La conversación recibe el mayor peso; una segunda superficie explica las funciones del último turno. El carácter es cercano y sereno, con información técnica legible.

Referencias visuales: [Landing](../../../../../Landing/index.html), [Landing extendida](../../../../../Landing/vera-landing.html) y Mockups en modo claro: «Vera - Rediseño-Acceso y bienvenida.pdf», «Vera - Rediseño-Tu dinero.pdf» y «Vera - Rediseño-Mover dinero y hablar con Vera.pdf», en `../../../../../Mockups/`.

La verdad implementada está en `web/index.html`, `web/estilos.css` y `web/app.js`. Los datos financieros y las propuestas son ficticios. Una herramienta validada o una recuperación del backend no prueban que el modelo haya completado la consulta.

**Key Characteristics:**

- Superficies claras, bordes finos y sombras ambientales suaves.
- Marca serif y abanico de tarjetas; lectura sans y trazas monoespaciadas.
- Conversación principal, procedencia y recuperación visibles.

## Colors

Los valores normativos están en el frontmatter. El cielo orienta la interacción; el agua señala resultados.

- **Primary:** tinta para Enviar y texto principal; cielo para foco, enlaces, transcripción y pregunta de ejemplo. El azul suave contiene el mensaje de la persona.
- **Secondary:** agua para Vera, resultados, procedencia de voz y propuesta simulada; sus tintes dan contexto sin competir con el diálogo.
- **Neutral:** perla como fondo, blanco como superficie y elevado como apoyo. Borde separa zonas; secundario y terciario mantienen notas y tiempos legibles.
- **Error:** vino sobre rosa suave para fallos y grabación activa, siempre con texto descriptivo.

**The Estado explícito Rule.** Acompañar todo color de éxito, espera, grabación o error con texto que explique lo que ocurre.

## Typography

Plus Jakarta Sans lleva cuerpo y controles; Playfair Display identifica la marca y «Vera» en el título. JetBrains Mono distingue funciones, JSON y tiempos. Se sirven WOFF2 locales en `/fuentes/`, con licencias SIL OFL; conservar la carga compatible con `font-src 'self'`.

El cuerpo base define la escala; mensajes, ayudas y controles tienen tamaños propios. El título se reduce en pantallas intermedias y llega a 32 px en móvil estrecho. JSON y tiempos conservan 12 px también en móvil; nombres de función usan 11 px. Evitar trasladar etiquetas pequeñas a contenido extenso. La introducción se limita a unas 65 unidades de carácter; consumo y tiempos usan cifras tabulares.

**The Traza legible Rule.** Ajustar las líneas largas del JSON y mantener su tamaño; reducir el ancho disponible nunca justifica encoger la información técnica.

## Layout

Cabecera y contenido comparten un máximo de 1320 px. Desde 821 px, dos columnas de proporción 1,4:1 separan conversación y traza con panel-gap. La traza permanece junto al diálogo con posición sticky, a 24 px desde arriba. Mensajes y eventos tienen desplazamiento independiente. La traza representa el último turno, no un flujo de eventos en vivo.

A 1050 px se reducen márgenes y separación. A 820 px se apilan las columnas, la traza deja de ser sticky y sus eventos crecen sin límite de altura. A 480 px se compactan cabecera y márgenes; la introducción envuelve, el selector se adapta y el campo de pregunta sube a 16 px. Los controles de voz envuelven sin ocultar estados ni reintentos. Conservar el ajuste de cadenas largas y ancho mínimo cero de las columnas.

## Elevation & Depth

Blanco sobre perla, bordes finos y sombra ambiental de panel (`0 16px 38px rgba(40,70,100,.055), 0 2px 6px rgba(40,70,100,.025)`). Enviar incorpora una elevación breve al pasar el puntero. Los degradados se concentran en marca y orbe. Las transiciones de controles duran 180 ms y se desactivan con `prefers-reduced-motion`.

## Shapes

Paneles de curvas amplias, ligeramente menores en móvil; campos y botones comparten la curva control. Micrófono y sugerencia son píldoras. El mensaje de la persona es una burbuja azul con esquina inferior contenida; Vera mantiene texto abierto sobre blanco. El JSON usa fondo tenue y borde fino.

El abanico de cabecera tiene tres tarjetas: tinta, cielo y agua. El orbe de bienvenida conserva la firma de Landing: esfera cielo/agua/índigo con dos tarjetas blancas superpuestas —una translúcida— y un punto índigo en la frontal. Ambos son decorativos y se ocultan a tecnologías de asistencia.

## Components

- **Cabecera y sesión:** marca enlazada al inicio, modo y consumo. Diferenciar turnos de solicitudes, y cuota local de saldo del proveedor. Nueva conversación limpia el diálogo y conserva consumo y esperas.
- **Botones y foco:** Enviar usa tinta; secundarios, blanco translúcido; Detener voz y Cancelar consulta son discretos. Foco cielo visible (3 px, separación de 4 px), hover y disabled explícitos. Conservar alturas táctiles ampliadas en móvil.
- **Pregunta y grabación:** etiqueta visible, máximo de 2000 caracteres, Enter envía y Shift+Enter añade línea. La demo usa pregunta y transcripción fijas. Hablar pide permiso, muestra contador y «Detener y enviar»; se detiene a los 20 s. Conservar clip reproducible, transcripción y bloqueo de entrada durante consulta. Nueva grabación limpia la transcripción previa.
- **Lectura:** texto disponible antes y durante el audio. Catalina, Gemini, Dora local y navegador dependen de disponibilidad; procedencia y consumo son visibles. Lectura automática y respaldo del navegador son opciones inicialmente activadas. Repetir la última voz reutiliza audio completo en memoria. Detener cancela solicitud y reproducción; autoplay bloqueado pide pulsar Escuchar. Los tiempos distinguen transcripción, respuesta y primer audio sin anunciar cada fragmento.
- **Traza y propuesta:** desplegables abiertos al recibir función, argumentos y resultado; JSON ajustado y pie con modelo e instrucciones. «Propuesta simulada» explica confirmación pendiente y ausencia de ejecución de dinero. Una recuperación se etiqueta «RESPUESTA DEL BACKEND» y conserva el motivo del fallo.
- **Errores y reintentos:** conservar pregunta y transcripción al reintentar respuesta, sin duplicar mensaje; si falla ASR, ofrecer el mismo clip. Mantener cancelación, límite local y esperas. Una pausa de TTS no bloquea chat. No ocultar fallos de voz cuando funciona el respaldo. Estadísticas distinguen generaciones, caché y fallos; no miden calidad ni audición.
- **Accesibilidad:** español, etiquetas persistentes, log y estados con anuncios corteses, errores con `role="alert"`, audio y desplegables nativos. La decoración no reemplaza nombres accesibles. Estos mecanismos no constituyen una auditoría con lector de pantalla.

## Do's and Don'ts

- **Do** mantener visibles modo, datos ficticios, procedencia de voz, consumo, espera y recuperación.
- **Do** conservar texto, micrófono, voz, cancelación y reintentos al extender el diseño.
- **Do** conservar fuentes locales, ambas firmas de marca y JSON/tiempos legibles en móvil.
- **Do** mantener claves de proveedores en el servidor; el token de acceso de sesión no es una clave del proveedor.
- **Don't** prometer respuesta del modelo garantizada ni presentar recuperación del backend como éxito del modelo.
- **Don't** presentar propuestas como transferencias ejecutadas, demo como transcripción real ni tope local como saldo de cuenta.
- **Don't** afirmar pruebas con API real, micrófono físico o lector de pantalla a partir de capturas o demo.
- **Don't** convertir audio o historial en archivos persistentes: se mantienen en memoria; en modo real el audio de transcripción se envía al proveedor.
