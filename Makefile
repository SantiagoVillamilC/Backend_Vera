# Atajos del backend. Los binarios viven en out/bin, que está en .gitignore.
BIN := out/bin
COMANDOS := api migrate enriquecer avisos semilla-inteligencia evaluar-clasificador rollback-m8

.PHONY: build verificar imagen demo limpiar

# Compila los siete comandos con CGO apagado, igual que la imagen de Docker.
build:
	@mkdir -p $(BIN)
	@for comando in $(COMANDOS); do \
		printf '  %-24s' "$$comando"; \
		CGO_ENABLED=0 go build -o "$(BIN)/$$comando" "./cmd/$$comando" || exit 1; \
		printf 'ok\n'; \
	done
	@printf '\n%s\n' "Binarios en $(BIN):"; ls -1 "$(BIN)" | sed 's/^/  /'

# Lo mismo que corre la CI: vet y pruebas con el detector de carreras.
verificar: build
	go vet ./...
	go test -race -count=1 ./...

# Imagen de despliegue con api y migrate, igual que la CI.
imagen:
	docker build -t vera-api:local .

# Levanta todo sobre una base desechable y comprueba la API de punta a punta. El script compila.
demo:
	./infra/local/demostrar.sh

# Borra los binarios y los contenedores de la demostración.
limpiar:
	rm -rf "$(BIN)"
	-docker rm -f vera-demo-pg vera-demo-redis
