default:
    just --list

up:
    docker compose up -d

up-app:
    docker compose --profile app up -d

down:
    docker compose down

run:
    go run ./cmd/api

test:
    go test ./...

build:
    go build ./...
