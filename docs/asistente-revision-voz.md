# Revisión de fiabilidad de voz · 21 de octubre de 2026

Problema observado: la voz natural recurría con frecuencia a SpeechSynthesis.
Una prueba directa de Interactions devolvió HTTP 429, `Retry-After: 7412` y sin
detalles RPC. Nuestro adaptador descartaba esa cabecera y asumía 60 segundos.
El cliente intentaba TTS antes de vencer la espera y volvía al navegador.

## Comportamiento corregido

1. Se respeta Retry-After en segundos o fecha HTTP. No se deduce una cuota diaria
   sólo porque la espera sea larga. Los errores SSE conservan cuota, credenciales
   o contenido según su código; nunca se muestran mensajes privados del proveedor.
2. Gemini sigue siendo la voz principal en modo real. Una pausa de cuota o fallo
   de disponibilidad previo al audio permite usar Dora, voz neural femenina en
   español de Kokoro-82M, en CPU local. No requiere otra API key ni facturación.
3. Dora también puede elegirse directamente. Transmite por frases, conserva las
   cifras del backend y no consume llamadas a Google. Su identidad/acento difiere
   de Aoede. Los pesos se verifican por SHA256, se cargan una vez y se calientan.
4. La voz del navegador queda como último respaldo. El cliente no impide usar
   Dora por una cuota de Gemini en pausa o presupuesto agotado, no cambia de voz
   por autoplay bloqueado y mantiene la opción Dora al cargar voces del sistema.
5. No se mezclan voces después de emitir PCM. Cancelar no reenvía audio; un
   solapamiento breve de la última frase en CPU admite hasta cuatro esperas de
   500 ms ante 409 local. No son llamadas adicionales a Google.
6. Se identifican fuente de voz, respaldo, generaciones completas, caché y fallos.
   Los contadores del servidor no miden audición, satisfacción ni calidad percibida.

## Evidencia final

Informe: [voz-natural-con-respaldo.json](evaluaciones/voz-natural-con-respaldo.json).
Veinte preguntas con Gemini de chat real y datos financieros ficticios, por las
rutas HTTP del laboratorio. Alterna diez respuestas WAV y diez progresivas.

| Medida | Resultado |
| --- | --- |
| Turnos respondidos | 20/20 |
| Audios completos, PCM16 mono 24 kHz válidos | 20/20 |
| Fuente Gemini TTS | 0 |
| Fuente Dora neural local | 20 |
| Respaldo del navegador | No forma parte del ejecutor HTTP; no fue necesario para los veinte audios |
| Solicitudes físicas a Google, incluido chat/herramientas y TTS | 29 |
| Mediana hasta primer fragmento, diez respuestas progresivas | 3030,5 ms |
| Rango hasta primer fragmento progresivo | 652–7098 ms |
| Mediana de generación completa, ambos modos | 6646,5 ms |

Son resultados de esta muestra, con la cuota TTS de Gemini pausada, no una
garantía futura del 100 %. El tiempo no incluye grabar, transcribir o reproducir
todo el audio, y depende de CPU, longitud y carga del equipo.

Se conservan dos ejecuciones anteriores: `voz-natural-primera-revision.json` y
`voz-natural-streaming-revision.json`. Cada una obtuvo diecinueve WAV locales
validados y un WAV de Gemini que el comprobador rechazó por sus restricciones
de formato. No se conservó ese audio completo para una revalidación posterior:
no se cuenta como aprobado. El validador posterior recorre bloques RIFF y admite
metadatos, sin asumir que `data` empieza en el byte 44. La muestra final evita
esa incertidumbre: los veinte audios locales se validaron por completo.

## Comprobaciones

- `go test -race ./...` y `go vet ./...`.
- Quince pruebas Node, incluidas cuota pausada con respaldo local, caché, voces
  del sistema, autoplay bloqueado y último respaldo del navegador.
- Cuatro pruebas Python: autenticación/validación, WAV/PCM, pronunciación de
  centavos y corte de streaming sin falso evento de finalización.
- Prueba HTTP de veinte turnos con proveedor TTS limitado y respaldo controlado:
  una sola llamada TTS a Google, veinte síntesis locales y chat independiente.
- Revisión en navegador: demo con audio neural real, reproducción progresiva y
  fuente Dora identificada. No acredita micrófono físico ni compatibilidad móvil.
- Generación del contrato sin cambios y lint del parche sin nuevos problemas.

La muestra [vera-dora-local.wav](evaluaciones/vera-dora-local.wav) contiene sólo
saldo ficticio del sandbox. Se exportó expresamente para comparar la voz; el
laboratorio habitual conserva audio e historial exclusivamente en memoria.

## Uso

Cierra la ventana del laboratorio anterior y vuelve a abrir `Probar-Vera-Voz.cmd`.
La primera preparación descarga pesos/dependencias; las siguientes reutilizan
la imagen. Mantén Docker Desktop abierto. Puedes elegir Aoede con respaldo
automático o Dora directamente para una identidad vocal constante sin cuota TTS.

El respaldo permite leer una respuesta existente sin capacidad de TTS de Google.
Chat y ASR siguen dependiendo de sus propias cuotas y disponibilidad. Docker
apagado, falta de recursos o audio bloqueado en el navegador siguen pudiendo fallar.
