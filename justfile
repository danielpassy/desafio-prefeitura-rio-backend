default:
    just --list

up:
    docker compose up -d

down:
    docker compose down

test:
    go test ./...

build:
    go build ./...