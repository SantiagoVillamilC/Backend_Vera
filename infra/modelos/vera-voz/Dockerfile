# vera-voz: transcripción local con faster-whisper small. Reutiliza el servidor de infra/stt-local
# (mismo contrato /healthz y /transcribir) y monta la caché de pesos en /pesos.
#
# La imagen no lleva pesos ni secretos: WHISPER_TOKEN llega por entorno y Hugging Face escribe la
# caché del modelo small en /pesos/huggingface, que es un volume. Sin GPU, corre en CPU (int8).
FROM python:3.12-slim-bookworm

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    HF_HOME=/pesos/huggingface \
    WHISPER_MODELO=small \
    WHISPER_DIRECCION=127.0.0.1:8767 \
    WHISPER_GPU=no

WORKDIR /opt/stt

RUN groupadd --gid 1000 vera \
    && useradd --uid 1000 --gid 1000 --no-create-home --shell /usr/sbin/nologin vera \
    && mkdir -p /pesos/huggingface /opt/stt \
    && chown -R vera:vera /pesos /opt/stt

COPY infra/stt-local/requirements.txt /opt/stt/requirements.txt
RUN pip install --no-cache-dir -r /opt/stt/requirements.txt

COPY infra/stt-local/servidor.py /opt/stt/servidor.py

USER 1000:1000

VOLUME ["/pesos"]

# El servidor carga el modelo antes de escuchar, así que el healthcheck falla mientras carga y pasa
# cuando responde /healthz con el token del entorno.
HEALTHCHECK --interval=10s --timeout=5s --start-period=120s --retries=18 \
    CMD python -c "import os,urllib.request;r=urllib.request.Request('http://127.0.0.1:8767/healthz',headers={'X-Vera-Token':os.environ['WHISPER_TOKEN']});urllib.request.urlopen(r,timeout=3)" || exit 1

EXPOSE 8767
ENTRYPOINT ["python", "/opt/stt/servidor.py"]
