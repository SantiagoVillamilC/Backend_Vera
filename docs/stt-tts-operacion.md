# Operación de la voz de Vera (Kokoro)

## Kokoro integrado

Configurar en la API `VERA_VOZ_TOKEN` (32 caracteres como mínimo) y opcionalmente
`VERA_VOZ_URL=http://127.0.0.1:8766`. El servidor de Kokoro debe recibir el mismo valor
como `VERA_LAB_TOKEN`. No se registran tokens ni texto de síntesis.

`POST /v1/vera/sintetizar` recibe `{"texto":"Hola, soy Vera."}`, sesión de usuario
y `X-Vera-Token`. Exige consentimiento del asistente y reutiliza la cuota de voz por
usuario: por defecto 10 solicitudes por hora, compartidas con el envío de voz.
Las solicitudes de caché también consumen cuota. Redis no disponible impide admitir
uso; esa situación responde 503, no se confunde con una caída del sintetizador.

Éxito: HTTP 200 con `Content-Type: audio/wav`, PCM16 mono a 24 kHz, voz `ef_dora`.
Fallo del sintetizador: HTTP 200 JSON con el envelope habitual, `data` igual a
`{"audio_disponible":false,"voz_local":true}` y cabecera `X-Vera-Audio: no-disponible`.
La aplicación conserva el mensaje recibido y usa su síntesis local. La integración
de reproducción en Flutter no forma parte de estos cambios del backend.

Límites: 8192 bytes de texto, plazo total de 25 segundos, WAV de aproximadamente
87 segundos como máximo. Caché en memoria por SHA-256 del texto, TTL de cinco minutos,
16 entradas (aproximadamente 64 MiB de audio). Se serializan las síntesis concurrentes,
se respeta la cancelación y nunca se cachean fallos. La caché es por instancia y se
pierde al reiniciar. Los WAV salen con `Cache-Control: no-store`.

## Verificación

```bash
go test -race ./...
go vet ./...
```

Las pruebas Go usan servicios HTTP de prueba. La prueba con el modelo real requiere la imagen
`infra/voz-local` y su token.
