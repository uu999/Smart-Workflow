SHELL := /bin/bash
GOPATH_BIN := $(shell go env GOPATH)/bin
export PATH := /opt/homebrew/bin:/usr/local/bin:$(PATH):$(GOPATH_BIN)

.PHONY: help build test run-server run-worker sidecar-install run-sidecar \
        sqlc migrate-up migrate-down infra-up infra-down infra-logs tidy

help:
	@echo "targets:"
	@echo "  build          go build ./..."
	@echo "  test           go test ./..."
	@echo "  tidy           go mod tidy"
	@echo "  sqlc           regenerate sqlc code"
	@echo "  infra-up       docker compose up mysql+redis"
	@echo "  infra-down     docker compose down"
	@echo "  migrate-up     goose up (needs infra-up)"
	@echo "  migrate-down   goose down"
	@echo "  run-server     run gin API server"
	@echo "  run-worker     run asynq worker (needs redis)"
	@echo "  sidecar-install create venv + install deps"
	@echo "  run-sidecar    run FastAPI sidecar"

build:
	go build ./...

test:
	go test ./...

tidy:
	go mod tidy

sqlc:
	sqlc generate

infra-up:
	docker compose -f deployments/docker-compose.yaml up -d

infra-down:
	docker compose -f deployments/docker-compose.yaml down

infra-logs:
	docker compose -f deployments/docker-compose.yaml logs -f

GOOSE_DSN := swf:swfpass@tcp(127.0.0.1:3308)/smart_workflow?parseTime=true

migrate-up:
	goose -dir migrations mysql "$(GOOSE_DSN)" up

migrate-down:
	goose -dir migrations mysql "$(GOOSE_DSN)" down

run-server:
	go run ./cmd/server

run-worker:
	go run ./cmd/worker

sidecar-install:
	cd sidecar && python3 -m venv .venv && ./.venv/bin/pip install -q --upgrade pip && ./.venv/bin/pip install -q -r requirements.txt

run-sidecar:
	cd sidecar && ./.venv/bin/uvicorn main:app --host 127.0.0.1 --port 8090
