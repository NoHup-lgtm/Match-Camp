APP := matchcamp
DATABASE_URL ?= postgres://matchcamp:matchcamp@localhost:5432/matchcamp?sslmode=disable

.PHONY: dev test build migrate-up migrate-down compose-up compose-down tidy sqlc

dev:
	go run ./cmd/api

test:
	go test ./...

build:
	go build -o bin/$(APP) ./cmd/api

tidy:
	go mod tidy

sqlc:
	sqlc generate

migrate-up:
	goose -dir migrations postgres "$(DATABASE_URL)" up

migrate-down:
	goose -dir migrations postgres "$(DATABASE_URL)" down

compose-up:
	podman compose up --build

compose-down:
	podman compose down
