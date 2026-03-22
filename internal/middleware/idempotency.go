package middleware

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/akylbek/payment-system/api-gateway/internal/interfaces"
	"github.com/akylbek/payment-system/api-gateway/internal/models"
	"github.com/akylbek/payment-system/api-gateway/internal/telemetry"
)

func IdempotencyMiddleware(redisClient *redis.Client, paymentRepo interfaces.PaymentRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := c.GetHeader("Idempotency-Key")
		if key == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Idempotency-Key header is required"})
			c.Abort()
			return
		}

		ctx := c.Request.Context()

		// Check Redis cache
		cached, err := redisClient.Get(ctx, fmt.Sprintf("idempotency:%s", key)).Result()
		if err == nil {
			var payment models.Payment
			if err := json.Unmarshal([]byte(cached), &payment); err != nil {
				telemetry.Logger.Error("Failed to unmarshal cached payment from Redis",
					zap.String("idempotency_key", key),
					zap.Error(err),
				)
			} else {
				c.JSON(http.StatusOK, payment)
				c.Abort()
				return
			}
		} else if err != redis.Nil {
			telemetry.Logger.Error("Failed to get idempotency key from Redis",
				zap.String("idempotency_key", key),
				zap.Error(err),
			)
		}

		// Check database
		// payment, err := paymentRepo.GetByIdempotencyKey(ctx, key)
		// if err != nil && err != redis.Nil {
		// 	telemetry.Logger.Error("Failed to check idempotency key in database",
		// 		zap.String("idempotency_key", key),
		// 		zap.Error(err),
		// 	)
		// }
		// if err == nil && payment != nil {
		// 	c.JSON(http.StatusOK, payment)
		// 	c.Abort()
		// 	return
		// }

		c.Set("idempotency_key", key)
		c.Next()
	}
}
