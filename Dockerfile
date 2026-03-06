FROM golang:1.22-alpine AS builder

WORKDIR /app

# Copy proto module (needed by replace directive in go.mod)
COPY proto/ /app/proto/

# Copy service source
COPY services/api-gateway/ /app/services/api-gateway/

WORKDIR /app/services/api-gateway

RUN go mod download && go mod tidy

RUN CGO_ENABLED=0 GOOS=linux go build -o api-gateway ./cmd/main.go

FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /root/

COPY --from=builder /app/services/api-gateway/api-gateway .

EXPOSE 8081

CMD ["./api-gateway"]
