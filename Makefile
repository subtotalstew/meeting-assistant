# Умный помощник для конспектирования встреч — типовые задачи разработки.
# Рецепты намеренно простые (одна команда в строке), чтобы работать и в sh, и в cmd.exe.

MODULE  := github.com/subtotalstew/meeting-assistant
VERSION ?= dev
LDFLAGS := -s -w -X main.version=$(VERSION)

ifeq ($(OS),Windows_NT)
BIN := bin/meeting-assistant.exe
else
BIN := bin/meeting-assistant
endif

.DEFAULT_GOAL := help

.PHONY: help build run test test-race cover vet fmt tidy lint db db-down db-reset ci

help: ## Показать список целей
	@echo "build      — собрать бинарник в $(BIN)"
	@echo "run        — запустить приложение"
	@echo "test       — unit- и интеграционные тесты (интеграционные пропускаются без TEST_DATABASE_URL)"
	@echo "test-race  — тесты под race detector"
	@echo "cover      — покрытие в coverage.out и сводка по функциям"
	@echo "vet        — go vet"
	@echo "fmt        — gofmt по всему модулю"
	@echo "tidy       — go mod tidy"
	@echo "lint       — vet + gofmt-проверка (как в CI)"
	@echo "db         — поднять PostgreSQL в Docker и дождаться healthcheck"
	@echo "db-down    — остановить контейнер"
	@echo "db-reset   — пересоздать базу с нуля"
	@echo "ci         — локальный прогон того же, что делает CI"

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/meeting-assistant

run:
	go run ./cmd/meeting-assistant

test:
	go test -count=1 ./...

test-race:
	go test -race -count=1 ./...

cover:
	go test -count=1 -coverprofile=coverage.out -covermode=atomic ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w .

tidy:
	go mod tidy

lint:
	go vet ./...
	test -z "$$(gofmt -l .)" || (gofmt -l . && exit 1)

db:
	docker compose up -d --wait

db-down:
	docker compose down

db-reset:
	docker compose down -v
	docker compose up -d --wait

ci:
	go build ./...
	go vet ./...
	go test -race -count=1 ./...
