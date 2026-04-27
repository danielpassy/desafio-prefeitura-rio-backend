set dotenv-load := true

default:
    just --list

up:
    docker compose up -d

up-app:
    docker compose --profile app up -d

down:
    docker compose down

migrate:
    go run ./cmd/migrate

run:
    go run ./cmd/api

test:
    go test ./...

test-compose:
    docker compose --profile test up --build --abort-on-container-exit --exit-code-from tests tests

smoke:
    docker compose --profile k6 run --rm -e K6_SCRIPT=smoke.js k6

load-test:
    docker compose --profile k6 run --rm -e K6_SCRIPT=load.js k6

build:
    go build ./...
