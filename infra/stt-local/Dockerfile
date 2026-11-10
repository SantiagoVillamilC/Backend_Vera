# Servicio de transcripción: sólo loopback interno, sin puerto publicado.
FROM python:3.12-slim

RUN apt-get update && apt-get install -y --no-install-recommends libgomp1 ffmpeg \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /opt/stt
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

# El modelo se descarga en el primer arranque a /modelos (volumen), no en la imagen.
ENV HF_HOME=/modelos HF_HUB_DISABLE_TELEMETRY=1 WHISPER_MODELO=small WHISPER_DIRECCION=0.0.0.0:8767

COPY servidor.py .
USER 10001
EXPOSE 8767
CMD ["python", "servidor.py"]
