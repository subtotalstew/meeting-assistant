# Умный помощник для конспектирования встреч — типовые задачи разработки.
# Рецепты намеренно простые (одна команда в строке), чтобы работать и в sh, и в cmd.exe.

MODULE  := github.com/subtotalstew/meeting-assistant
VERSION ?= dev
LDFLAGS := -s -w -X main.version=$(VERSION)

# Версия линтера пинится той же, что в .github/workflows/ci.yml: локальный
# `make lint` и CI должны ругаться одинаково.
GOLANGCI_VERSION := v2.15.0

ifeq ($(OS),Windows_NT)
BIN := bin/meeting-assistant.exe
LINT := $(shell go env GOPATH)\bin\golangci-lint.exe
# На Windows make не находит sh: GNU Make из Equation Solution запускает команды
# через cmd.exe, поэтому проверки пишутся его синтаксисом.
NO_FMT = gofmt -l . 1>"%TEMP%\ma-gofmt.txt"& for /f %%f in ("%TEMP%\ma-gofmt.txt") do @if %%~zf==0 (del "%TEMP%\ma-gofmt.txt") else (type "%TEMP%\ma-gofmt.txt" & del "%TEMP%\ma-gofmt.txt" & exit /b 1)
HAS_LINT = if not exist "$(LINT)" (echo golangci-lint не найден: выполните make lint-install & exit /b 1)
else
BIN := bin/meeting-assistant
LINT := $(shell go env GOPATH)/bin/golangci-lint
NO_FMT = test -z "$$(gofmt -l .)" || (gofmt -l . && exit 1)
HAS_LINT = test -x "$(LINT)" || (echo "golangci-lint не найден: выполните make lint-install" && exit 1)
endif

.DEFAULT_GOAL := help

.PHONY: help build run test test-race cover vet fmt tidy lint lint-install db db-down db-reset ci

help: ## Показать список целей
	@echo "build        — собрать бинарник в $(BIN)"
	@echo "run          — запустить приложение"
	@echo "test         — unit- и интеграционные тесты (интеграционные пропускаются без TEST_DATABASE_URL)"
	@echo "test-race    — тесты под race detector"
	@echo "cover        — покрытие в coverage.out и сводка по функциям"
	@echo "vet          — go vet"
	@echo "fmt          — gofmt по всему модулю"
	@echo "tidy         — go mod tidy"
	@echo "lint         — gofmt + go vet + golangci-lint (как в CI)"
	@echo "lint-install — поставить golangci-lint $(GOLANGCI_VERSION)"
	@echo "db           — поднять PostgreSQL в Docker и дождаться healthcheck"
	@echo "db-down      — остановить контейнер"
	@echo "db-reset     — пересоздать базу с нуля"
	@echo "ci           — локальный прогон того же, что делает CI"

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

# lint повторяет шаги джобы lint из CI: gofmt, go vet и golangci-lint.
# Если линтера нет, цель падает с подсказкой, а не молча проверяет меньше.
lint:
	@$(NO_FMT)
	go vet ./...
	@$(HAS_LINT)
	"$(LINT)" run --timeout=5m ./...

lint-install:
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION)

db:
	docker compose up -d --wait

db-down:
	docker compose down

db-reset:
	docker compose down -v
	docker compose up -d --wait

ci: lint build test-race

