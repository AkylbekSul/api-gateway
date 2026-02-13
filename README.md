# API Gateway Service

The API Gateway serves as the main entry point for all payment-related requests in the distributed payment system. It handles client requests, provides idempotency guarantees, and publishes payment events to downstream services.

## Overview

The API Gateway is responsible for:
- Receiving and validating payment creation requests
- Ensuring idempotent payment processing using Redis
- Storing payment records in PostgreSQL
- Publishing payment events to Kafka for processing by other services
- Providing RESTful APIs for payment management
- Distributed tracing and observability with OpenTelemetry

## Architecture

```
Client Request → API Gateway → PostgreSQL (persistence)
                     ↓
                   Redis (idempotency cache)
                     ↓
                   Kafka (event publishing)
```

## Technology Stack

- **Language**: Go 1.21+
- **Web Framework**: Gin
- **Database**: PostgreSQL
- **Cache**: Redis
- **Message Broker**: Kafka
- **Observability**: OpenTelemetry + Jaeger
- **Logging**: Zap (Uber)

## Configuration

The service uses environment variables for configuration:

| Variable | Description | Default |
|----------|-------------|---------|
| `PORT` | HTTP server port | `8081` |
| `DATABASE_URL` | PostgreSQL connection string | Required |
| `REDIS_URL` | Redis server address | Required |
| `KAFKA_BROKERS` | Kafka broker addresses | Required |
| `JAEGER_ENDPOINT` | Jaeger collector endpoint | Optional |

## API Endpoints

### Create Payment
```http
POST /api/v1/payments
Content-Type: application/json
X-Idempotency-Key: <unique-key>

{
  "amount": 100.50,
  "currency": "USD",
  "customer_id": "customer-123",
  "merchant_id": "merchant-456"
}
```

**Response**: `201 Created`
```json
{
  "id": "payment-uuid",
  "amount": 100.50,
  "currency": "USD",
  "customer_id": "customer-123",
  "merchant_id": "merchant-456",
  "status": "NEW",
  "idempotency_key": "unique-key",
  "created_at": "2026-02-13T10:00:00Z"
}
```

### Get Payment
```http
GET /api/v1/payments/:id
```

**Response**: `200 OK`
```json
{
  "id": "payment-uuid",
  "amount": 100.50,
  "currency": "USD",
  "status": "PROCESSED",
  "created_at": "2026-02-13T10:00:00Z"
}
```

### Confirm Payment
```http
POST /api/v1/payments/:id/confirm
```

**Response**: `200 OK`
```json
{
  "status": "confirmed",
  "payment_id": "payment-uuid"
}
```

### Health Check
```http
GET /health
```

### Metrics
```http
GET /metrics
```

## Key Features

### Idempotency
The API Gateway ensures idempotent payment creation using the `X-Idempotency-Key` header. This prevents duplicate payments if the same request is sent multiple times:
- Middleware intercepts requests and checks Redis for existing responses
- Cached responses are returned immediately
- New requests are processed and cached for 24 hours

### Event Publishing
After successfully creating a payment, the gateway publishes a `payment.created` event to Kafka:
```json
{
  "payment_id": "payment-uuid",
  "amount": 100.50,
  "currency": "USD",
  "customer_id": "customer-123",
  "merchant_id": "merchant-456",
  "status": "NEW",
  "created_at": "2026-02-13T10:00:00Z"
}
```

### Distributed Tracing
All requests are traced using OpenTelemetry with trace context propagation:
- Automatic span creation for incoming requests
- Trace IDs logged for correlation
- Integration with Jaeger for visualization

## Running the Service

### Local Development

1. Set up environment variables:
```bash
export DATABASE_URL="postgresql://user:password@localhost:5432/api_gateway?sslmode=disable"
export REDIS_URL="localhost:6379"
export KAFKA_BROKERS="localhost:9092"
export PORT="8081"
```

2. Run database migrations:
```bash
# Apply migrations from migrations/ directory
```

3. Start the service:
```bash
go run cmd/main.go
```

### Docker

Build and run with Docker:
```bash
docker build -t api-gateway .
docker run -p 8081:8081 \
  -e DATABASE_URL="..." \
  -e REDIS_URL="..." \
  -e KAFKA_BROKERS="..." \
  api-gateway
```

### Docker Compose

Run as part of the complete system:
```bash
docker-compose up api-gateway
```

## Dependencies

### External Services
- **PostgreSQL**: Payment data persistence
- **Redis**: Idempotency key caching
- **Kafka**: Event streaming to downstream services
- **Jaeger** (optional): Distributed tracing

### Downstream Consumers
- **Payment Orchestrator**: Consumes `payment.created` events
- **Ledger Service**: Eventually receives payment state changes

## Database Schema

The service uses the following main table:

```sql
CREATE TABLE payments (
    id VARCHAR(255) PRIMARY KEY,
    amount DECIMAL(10,2) NOT NULL,
    currency VARCHAR(3) NOT NULL,
    customer_id VARCHAR(255) NOT NULL,
    merchant_id VARCHAR(255) NOT NULL,
    status VARCHAR(50) NOT NULL,
    idempotency_key VARCHAR(255) UNIQUE,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

## Monitoring

### Metrics
Prometheus metrics are exposed at `/metrics` endpoint:
- HTTP request duration histograms
- Request count by status code
- Active connections
- Custom business metrics

### Logging
Structured logging with Zap:
- JSON format for production
- Trace ID correlation
- Error stack traces
- Request/response logging

### Tracing
OpenTelemetry traces include:
- HTTP request spans
- Database query spans
- Redis operations
- Kafka publish operations

## Error Handling

The service returns standard HTTP status codes:
- `200 OK`: Successful operation
- `201 Created`: Payment successfully created
- `400 Bad Request`: Invalid request payload
- `404 Not Found`: Payment not found
- `409 Conflict`: Idempotency key conflict
- `500 Internal Server Error`: Server-side errors

## Development

### Project Structure
```
api-gateway/
├── cmd/
│   └── main.go              # Application entry point
├── internal/
│   ├── api/
│   │   └── router.go        # Route definitions
│   ├── config/
│   │   └── config.go        # Configuration management
│   ├── handlers/
│   │   └── payment_handler.go  # HTTP handlers
│   ├── interfaces/
│   │   └── payment_repository.go  # Repository interface
│   ├── middleware/
│   │   └── idempotency.go   # Idempotency middleware
│   ├── models/
│   │   └── payment.go       # Domain models
│   ├── repository/
│   │   └── payment_repository.go  # Data access layer
│   └── telemetry/
│       └── telemetry.go     # Observability setup
├── migrations/
│   └── 001_api_gateway_schema.sql
├── Dockerfile
└── README.md
```

### Running Tests
```bash
go test ./...
```

## Graceful Shutdown

The service supports graceful shutdown with a 5-second timeout:
- Stops accepting new requests
- Completes in-flight requests
- Closes database connections
- Flushes telemetry data
- Closes Kafka writer

## License

Copyright © 2026
