GO      := go
BIN     := bin
MIGRATE := $(GO) run ./cmd/migrate

# Each command reads .env itself (internal/platform/config), so nothing here has to source it.
# Put real credentials in .env (gitignored), never in this file.

.PHONY: build check fmt vet test tidy run seed migrate-up migrate-down clean

build:
	$(GO) build -o $(BIN)/api ./cmd/api

check: fmt vet test

fmt:
	gofmt -l -w ./cmd ./internal

vet:
	$(GO) vet ./...

test:
	$(GO) test ./...

tidy:
	$(GO) mod tidy

run:
	$(GO) run ./cmd/api

seed:
	$(GO) run ./cmd/devseed

migrate-up:
	$(MIGRATE) -direction up

migrate-down:
	$(MIGRATE) -direction down -steps 1 -allow-destructive

clean:
	rm -rf $(BIN)
