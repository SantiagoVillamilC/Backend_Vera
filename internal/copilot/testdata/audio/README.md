# Clips de prueba

Son tonos o silencio sintéticos, sin voces ni información personal. Se generaron con
FFmpeg 8.1.1; los tests sólo leen los archivos y no necesitan FFmpeg instalado.

```sh
ffmpeg -f lavfi -i 'sine=frequency=440:sample_rate=16000:duration=0.2' -c:a pcm_s16le -map_metadata -1 tono.wav
ffmpeg -f lavfi -i 'sine=frequency=440:sample_rate=16000:duration=0.2' -c:a aac -b:a 24k -map_metadata -1 tono.m4a
ffmpeg -f lavfi -i 'anullsrc=r=16000:cl=mono' -t 0.2 -c:a pcm_s16le -map_metadata -1 silencio.wav
ffmpeg -f lavfi -i 'anullsrc=r=16000:cl=mono' -t 60 -c:a aac -b:a 8k -map_metadata -1 limite.m4a
ffmpeg -f lavfi -i 'anullsrc=r=8000:cl=mono' -t 61 -c:a aac -b:a 8k -map_metadata -1 largo.m4a
```

Las respuestas de transcripción se fijan en los proveedores simulados; el tono no se
usa como prueba de reconocimiento real de palabras.
