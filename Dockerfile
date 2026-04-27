FROM golang:1.25-alpine AS deps
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .

FROM deps AS test
CMD ["go", "test", "./...", "-count=1"]

FROM deps AS builder
RUN CGO_ENABLED=0 go build -o /app/api ./cmd/api

FROM alpine:3.21
RUN apk --no-cache add ca-certificates
WORKDIR /app
ENV GIN_MODE=release
COPY --from=builder /app/api .
EXPOSE 8080
ENTRYPOINT ["/app/api"]
