FROM golang:1.27.0 AS toolchain
FROM python:3.12.12-slim-bookworm
COPY --from=toolchain /usr/local/go /usr/local/go
ENV PATH="/usr/local/go/bin:${PATH}" PYTHONDONTWRITEBYTECODE=1
COPY requirements.txt descargar.py /opt/voz/
RUN pip install --no-cache-dir -r /opt/voz/requirements.txt
RUN python /opt/voz/descargar.py
COPY servidor.py iniciar.sh /opt/voz/
ENV GOPATH=/go
ENTRYPOINT ["sh", "/opt/voz/iniciar.sh"]
