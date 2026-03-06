package api

import (
	"encoding/json"
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"

	"github.com/akylbek/payment-system/api-gateway/internal/handlers"
	"github.com/akylbek/payment-system/api-gateway/internal/interfaces"
	"github.com/akylbek/payment-system/api-gateway/internal/middleware"
	"github.com/akylbek/payment-system/api-gateway/internal/telemetry"
	paymentpb "github.com/akylbek/payment-system/proto/payment"
)

func NewRouter(paymentRepo interfaces.PaymentRepository, redisClient *redis.Client, orchestratorClient paymentpb.PaymentOrchestratorClient) http.Handler {
	mux := http.NewServeMux()

	// Prometheus metrics
	mux.Handle("GET /metrics", promhttp.Handler())

	// Health check
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok", "service": "api-gateway"})
	})

	// Payment routes
	paymentHandler := handlers.NewPaymentHandler(paymentRepo, redisClient, orchestratorClient)

	// POST /payments with idempotency middleware
	idempotency := middleware.IdempotencyMiddleware(redisClient, paymentRepo)
	mux.Handle("POST /payments", idempotency(http.HandlerFunc(paymentHandler.CreatePayment)))
	mux.HandleFunc("GET /payments/{id}", paymentHandler.GetPayment)
	mux.HandleFunc("POST /payments/{id}/confirm", paymentHandler.ConfirmPayment)

	// Apply global middleware
	var handler http.Handler = mux
	handler = telemetry.TracingMiddleware(handler)
	handler = telemetry.RecoveryMiddleware(handler)

	return handler
}
