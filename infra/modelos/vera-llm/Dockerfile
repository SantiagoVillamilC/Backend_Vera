# vera-llm: razonador local con Ollama, pesos montados y nunca dentro de la imagen.
#
# La versión de Ollama está fijada a la del lockfile (0.40.10) y los GGUF se montan desde el volumen
# `pesos` en /pesos/modelos. La imagen no lleva pesos ni secretos: el modelo se elige por entorno
# (OLLAMA_MODELO) y el token de la API vive fuera de esta imagen.
FROM ollama/ollama:0.40.10

ENV OLLAMA_MODELS=/pesos/modelos \
    OLLAMA_HOST=127.0.0.1:11434 \
    OLLAMA_KEEP_ALIVE=24h

# Usuario sin root: el uid 1000 es el que docker-compose monta en /pesos. El volume se crea con ese
# dueño en el compose; si el host lo crea antes, ajusta `chown 1000:1000`.
RUN mkdir -p /pesos/modelos /pesos/ollama && chown -R 1000:1000 /pesos
USER 1000:1000

VOLUME ["/pesos"]

# Falla mientras el servidor no responde; el modelo se carga en el primer /api/generate. El
# start-period cubre el arranque del servidor y la carga del GGUF en el primer uso.
HEALTHCHECK --interval=10s --timeout=5s --start-period=120s --retries=12 \
    CMD ollama list >/dev/null 2>&1 || exit 1

EXPOSE 11434
ENTRYPOINT ["/bin/ollama"]
CMD ["serve"]
