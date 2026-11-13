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

SHA256 y tamaño de los dos assets están en `modelos/lock.json` y en el manifiesto
`descargar.py`, que los comprueba durante el build. Una prueba de `cmd/verificar-modelos`
cruza ambas fuentes y falla si divergen, así que no se repiten aquí.

El contenedor carga los modelos al inicio y calienta una frase antes de abrir el
laboratorio. El sintetizador escucha sólo en loopback interno, exige token
efímero y no conserva audio ni texto en archivos. La imagen incluye Go 1.27.0
y Python 3.12.12. La clave de Gemini sólo se usa en el proceso Go.
