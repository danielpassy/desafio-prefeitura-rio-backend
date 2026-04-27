FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY main.go ./
RUN CGO_ENABLED=0 go build -o /mock-idp .

FROM scratch
COPY --from=build /mock-idp /mock-idp
EXPOSE 8080
ENTRYPOINT ["/mock-idp"]
