# vera-kokoro: voz neural local (Kokoro-82M, voz ef_dora) como imagen aparte.
#
# Va separada de vera-voz por dos razones: la licencia GPL-3.0 de `phonemizer` queda confinada a esta
# imagen, y un fallo del sintetizador no tumba la transcripción de Whisper.
#
# Reutiliza sin cambios el código de infra/voz-local (servidor.py, descargar.py, requirements.txt).
# El contexto de build es la raíz del repo, igual que vera-voz y vera-llm.
#
# Los pesos (unos 0,14 GB) se descargan durante el build con SHA256 y tamaño verificados por
# descargar.py, que es el mismo manifiesto que cruza cmd/verificar-modelos con modelos/lock.json. Por
# eso esta imagen sí lleva pesos, a diferencia de vera-llm y vera-voz. El token no se hornea:
# VERA_LAB_TOKEN llega por entorno.
FROM python:3.12.12-slim-bookworm

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1

WORKDIR /opt/voz

COPY infra/voz-local/requirements.txt infra/voz-local/descargar.py /opt/voz/
RUN pip install --no-cache-dir -r /opt/voz/requirements.txt \
    && python /opt/voz/descargar.py

COPY infra/voz-local/servidor.py /opt/voz/servidor.py

RUN groupadd --gid 1000 vera \
    && useradd --uid 1000 --gid 1000 --no-create-home --shell /usr/sbin/nologin vera
USER 1000:1000

# servidor.py escucha en 127.0.0.1:8766 y rechaza un token de menos de 32 caracteres. El healthcheck
# falla mientras carga ONNX y calienta la primera frase, y pasa cuando responde /healthz.
HEALTHCHECK --interval=10s --timeout=5s --start-period=60s --retries=12 \
    CMD python -c "import os,urllib.request;r=urllib.request.Request('http://127.0.0.1:8766/healthz',headers={'X-Vera-Token':os.environ['VERA_LAB_TOKEN']});urllib.request.urlopen(r,timeout=3)" || exit 1

EXPOSE 8766
ENTRYPOINT ["python", "/opt/voz/servidor.py"]
