# Voz neural local del laboratorio

- Motor: `kokoro-onnx 0.6.1`, [MIT](https://github.com/thewh1teagle/kokoro-onnx/blob/main/LICENSE).
- Modelo: [Kokoro-82M v1.0](https://huggingface.co/hexgrad/Kokoro-82M), Apache-2.0.
- Voz: `ef_dora`, femenina, español, del conjunto de voces v1.0. Identidad y
  acento distintos de Aoede; no se promete equivalencia de calidad.
- Exportación ONNX y voces: [release model-files-v1.1](https://github.com/thewh1teagle/kokoro-onnx/releases/tag/model-files-v1.1).
- Fonemización: `phonemizer`, GPL-3.0; `espeakng-loader` contiene el runtime de
  eSpeak NG. Sus avisos y licencias se incluyen con las distribuciones Python.
- Dependencias directas y transitivas fijadas en `requirements.txt`; no se cambian
  dependencias del backend Go. Pesos descargados durante el build, fuera de Git.

SHA256 y tamaño de los assets (`kokoro-v1.0.int8.onnx` y `voices-v1.0.bin` por defecto, más
`kokoro-v1.0.onnx` y `kokoro-v1.0.fp16.onnx` con `VERA_VOZ_DESCARGAR_GPU=1`) están en
`modelos/lock.json` y en el manifiesto `descargar.py`, que los comprueba durante el build. Una
prueba de `cmd/verificar-modelos` cruza ambas fuentes y falla si divergen, así que no se repiten
aquí. Los pesos fp32 y fp16 pertenecen a la misma release y licencias que el int8.

Variante opcional en GPU: `servidor.py` con `VERA_VOZ_DISPOSITIVO=cuda` usa `onnxruntime-gpu` y
las librerías de NVIDIA fijadas en `requirements-cuda.txt`, que no se instalan en la imagen CPU.
Esas librerías (CUDA runtime, cuDNN, cuFFT, cuRAND, NVRTC, cuBLAS y nvJitLink) se distribuyen
bajo licencias de NVIDIA, no bajo MIT ni Apache-2.0. Revisa sus términos antes de redistribuir
una imagen que las incluya.

El contenedor carga los modelos al inicio y calienta una frase antes de abrir el
laboratorio. El sintetizador escucha sólo en loopback interno, exige token
efímero y no conserva audio ni texto en archivos. La imagen incluye Go 1.27.0
y Python 3.12.12. La clave de Gemini sólo se usa en el proceso Go.
