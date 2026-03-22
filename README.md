# API Gateway Service

The API Gateway is the main entry point for all payment-related requests in the distributed payment system. It receives client requests, validates them, ensures idempotent processing, persists payment records, and coordinates with downstream services via gRPC.

## Architecture

```
Client Request → API Gateway → PostgreSQL (persistence)
                     ↓
                   Redis (idempotency cache)
                     ↓
                   Payment Orchestrator (gRPC + Circuit Breaker)
```

### Request Flow

```
Client Request
    ↓
Tracing Middleware (creates span, propagates trace context)
    ↓
Idempotency Middleware (checks Redis cache)
    ├─ Cache Hit → Return cached response
    └─ Cache Miss → Continue
    ↓
Payment Handler
    ├─ Validate request
    ├─ Generate UUID
    ├─ Save to PostgreSQL (status: NEW)
    ├─ Cache in Redis (24h TTL)
    ├─ Send to Orchestrator via gRPC (circuit breaker protected)
    └─ Return 201 Created
```

## Technology Stack

- **Language**: Go 1.22
- **Web Framework**: Gin
- **Database**: PostgreSQL
- **Cache**: Redis
- **Service Communication**: gRPC (Payment Orchestrator)
- **Resilience**: Sony gobreaker (circuit breaker) + retry with exponential backoff
- **Observability**: OpenTelemetry + Jaeger
- **Logging**: Zap
- **Metrics**: Prometheus

## Configuration

Environment variables:

| Variable | Description | Default |
|----------|-------------|---------|
| `PORT` | HTTP server port | `8081` |
| `DATABASE_URL` | PostgreSQL connection string | Required |
| `REDIS_URL` | Redis server address | Required |
| `ORCHESTRATOR_GRPC_ADDR` | gRPC endpoint for Payment Orchestrator | `localhost:50051` |
| `JAEGER_ENDPOINT` | Jaeger OTLP HTTP collector | `jaeger:4318` |

## API Endpoints

### Create Payment
```http
POST /payments
Content-Type: application/json
Idempotency-Key: <unique-key>

{
  "amount": 100.50,
  "currency": "USD",
  "customer_id": "customer-123",
  "merchant_id": "merchant-456"
}
```

**Response** `201 Created`:
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
GET /payments/:id
```

### Confirm Payment
```http
POST /payments/:id/confirm
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

The `Idempotency-Key` header is required for payment creation. The middleware checks Redis for an existing cached response:
- **Cache hit**: returns the cached response immediately
- **Cache miss**: processes the request and caches the response for 24 hours

### Circuit Breaker

The gRPC client to the Payment Orchestrator is wrapped with a circuit breaker (Sony gobreaker) to prevent cascading failures:

| Parameter | Value |
|-----------|-------|
| Failure threshold | 60% failure ratio on 5+ requests |
| Open → Half-Open timeout | 15 seconds |
| Max requests in Half-Open | 3 |
| Evaluation interval | 10 seconds |

**States**:
- **Closed** (normal): requests pass through, failures are tracked
- **Open** (failure detected): requests rejected immediately with `503 Service Unavailable`
- **Half-Open** (recovery): up to 3 test requests allowed to check if the service recovered

The gRPC client also includes retry logic (max 1 retry, exponential backoff starting at 100ms) for transient errors (`Unavailable`, `DeadlineExceeded`, `ResourceExhausted`).

### Distributed Tracing

OpenTelemetry integration with Jaeger:
- Automatic span creation for HTTP requests
- Trace ID in structured logs and `X-Trace-ID` response header
- Trace context propagation across services

## Database Schema

The main `payments` table:

```sql
CREATE TABLE payments (
    id VARCHAR(255) PRIMARY KEY,
    amount DECIMAL(15,2) NOT NULL,
    currency VARCHAR(3) NOT NULL DEFAULT 'USD',
    customer_id VARCHAR(255) NOT NULL,
    merchant_id VARCHAR(255) NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'NEW',
    idempotency_key VARCHAR(255) UNIQUE,
    payment_method VARCHAR(50),
    description TEXT,
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);
```

Additional tables: `customers`, `merchants`, `schema_migrations`. See [migrations/001_api_gateway_schema.sql](migrations/001_api_gateway_schema.sql) for the full schema.

## Running the Service

### Local Development

```bash
export DATABASE_URL="postgresql://user:password@localhost:5432/api_gateway?sslmode=disable"
export REDIS_URL="localhost:6379"
export ORCHESTRATOR_GRPC_ADDR="localhost:50051"

go run cmd/main.go
```

### Docker

```bash
docker build -t api-gateway .
docker run -p 8081:8081 \
  -e DATABASE_URL="..." \
  -e REDIS_URL="..." \
  -e ORCHESTRATOR_GRPC_ADDR="..." \
  api-gateway
```

## Error Handling

| Status | Meaning |
|--------|---------|
| `200 OK` | Successful operation |
| `201 Created` | Payment created |
| `400 Bad Request` | Invalid request payload |
| `404 Not Found` | Payment not found |
| `500 Internal Server Error` | Server-side error |
| `503 Service Unavailable` | Circuit breaker open |

## Project Structure

```
api-gateway/
├── cmd/
│   └── main.go                    # Entry point
├── internal/
│   ├── api/
│   │   └── router.go              # Route definitions & middleware setup
│   ├── circuitbreaker/
│   │   └── orchestrator_client.go # gRPC client with circuit breaker & retry
│   ├── config/
│   │   └── config.go              # Configuration (env vars)
│   ├── handlers/
│   │   └── payment_handler.go     # HTTP handlers
│   ├── interfaces/
│   │   └── payment_repository.go  # Repository interface
│   ├── middleware/
│   │   └── idempotency.go         # Idempotency middleware
│   ├── models/
│   │   └── payment.go             # Domain models
│   ├── repository/
│   │   └── payment_repository.go  # PostgreSQL data access
│   └── telemetry/
│       └── telemetry.go           # OpenTelemetry & Zap setup
├── migrations/
│   └── 001_api_gateway_schema.sql
├── Dockerfile
└── README.md
```

## Graceful Shutdown

The service shuts down gracefully with a 5-second timeout: stops accepting new requests, completes in-flight requests, closes database connections, flushes telemetry data, and syncs the logger.
