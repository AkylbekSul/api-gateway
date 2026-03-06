package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/redis/go-redis/v9"

	"github.com/akylbek/payment-system/api-gateway/internal/interfaces"
	"github.com/akylbek/payment-system/api-gateway/internal/models"
)

// contextKey is the context key type for idempotency
type contextKey string

const idempotencyKeyCtx contextKey = "idempotency_key"

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func IdempotencyMiddleware(redisClient *redis.Client, paymentRepo interfaces.PaymentRepository) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.Header.Get("Idempotency-Key")
			if key == "" {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Idempotency-Key header is required"})
				return
			}

			ctx := r.Context()

			// Check Redis cache
			cached, err := redisClient.Get(ctx, fmt.Sprintf("idempotency:%s", key)).Result()
			if err == nil {
				var payment models.Payment
				if err := json.Unmarshal([]byte(cached), &payment); err == nil {
					writeJSON(w, http.StatusOK, payment)
					return
				}
			}

			// Check database
			payment, err := paymentRepo.GetByIdempotencyKey(ctx, key)
			if err == nil && payment != nil {
				writeJSON(w, http.StatusOK, payment)
				return
			}

			ctx = context.WithValue(ctx, idempotencyKeyCtx, key)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
