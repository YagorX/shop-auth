FROM golang:1.25.0 AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/auth-service ./cmd/sso

FROM alpine:3.20

WORKDIR /app

RUN apk add --no-cache ca-certificates \
    && addgroup -S app \
    && adduser -S -G app app

COPY --from=builder /out/auth-service /app/auth-service
COPY config/config.docker.yaml /app/config/config.docker.yaml
COPY migrations /app/migrations

RUN chown -R app:app /app

EXPOSE 8082 44044

ENV CONFIG_PATH=/app/config/config.docker.yaml

USER app

ENTRYPOINT ["/app/auth-service"]
