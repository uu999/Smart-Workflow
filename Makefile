SHELL := /bin/bash
GOPATH_BIN := $(shell go env GOPATH)/bin
export PATH := /opt/homebrew/bin:/usr/local/bin:$(PATH):$(GOPATH_BIN)

COMPOSE := docker compose -f deployments/docker-compose.yaml

.PHONY: help build build-swf test run-server run-worker sidecar-install run-sidecar \
        sqlc migrate-up migrate-down infra-up infra-down infra-logs tidy \
        docker-build up-all down-all logs-all

help:
	@echo "targets:"
	@echo "  build          go build ./..."
	@echo "  build-swf      build swf CLI binary to ./bin/swf"
	@echo "  test           go test ./..."
	@echo "  tidy           go mod tidy"
	@echo "  sqlc           regenerate sqlc code"
	@echo "  infra-up       docker compose up mysql+redis only"
	@echo "  infra-down     docker compose down"
	@echo "  migrate-up     goose up (local, needs infra-up)"
	@echo "  migrate-down   goose down"
	@echo "  run-server     run gin API server (local)"
	@echo "  run-worker     run asynq worker (local, needs redis)"
	@echo "  sidecar-install create venv + install deps"
	@echo "  run-sidecar    run FastAPI sidecar (local)"
	@echo "  --- containerized (one-command five-piece) ---"
	@echo "  docker-build   build all images (server/worker/swf/sidecar/migrate)"
	@echo "  up-all         start full stack (build + up -d, auto-migrate)"
	@echo "  down-all       stop full stack (keep volumes)"
	@echo "  logs-all       tail all service logs"

build:
	go build ./...

build-swf:
	go build -trimpath -ldflags "-s -w" -o bin/swf ./cmd/swf

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

# --- 容器化：一键起五件套（server/worker/sidecar/mysql/redis + migrate 建表）---

docker-build:
	$(COMPOSE) build

up-all:
	$(COMPOSE) up -d --build

down-all:
	$(COMPOSE) down

logs-all:
	$(COMPOSE) logs -f
