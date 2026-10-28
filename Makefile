BINARY := bin/hexlet-go-crawler

.PHONY: install build run test test-update lint lint-fix check

install:
	go mod download

build:
	go build -o $(BINARY) ./cmd/hexlet-go-crawler

# make run URL=https://example.com [ARGS="--depth 2 --workers 8"]; без URL печатает справку.
run:
ifeq ($(strip $(URL)),)
	go run ./cmd/hexlet-go-crawler --help
else
	go run ./cmd/hexlet-go-crawler $(ARGS) $(URL)
endif

test:
	go test -race -count=1 ./...

# Перезаписать эталонный отчёт crawler/testdata/report.json после осознанного изменения формата.
test-update:
	go test ./crawler -run TestAnalyzeMatchesGoldenReport -update

lint:
	golangci-lint run ./...

lint-fix:
	golangci-lint run --fix ./...

check: lint test
