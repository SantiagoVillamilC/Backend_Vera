SERVICIOS := simulador-riel simulador-custodia simulador-pse

.PHONY: build test vet up down logs

build:
	@for s in $(SERVICIOS); do \
		echo "go build ./cmd/$$s"; \
		go build -o /dev/null ./cmd/$$s || exit 1; \
	done

test:
	go test ./...

vet:
	go vet ./...

up:
	cd deploy && docker compose --env-file .env up -d --build

down:
	cd deploy && docker compose --env-file .env down

logs:
	cd deploy && docker compose --env-file .env logs -f
