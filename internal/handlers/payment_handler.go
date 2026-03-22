package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/akylbek/payment-system/api-gateway/internal/circuitbreaker"
	"github.com/akylbek/payment-system/api-gateway/internal/interfaces"
	"github.com/akylbek/payment-system/api-gateway/internal/models"
	"github.com/akylbek/payment-system/api-gateway/internal/telemetry"
	paymentpb "github.com/akylbek/payment-system/proto/payment"
)

type PaymentHandler struct {
	repo               interfaces.PaymentRepository
	redisClient        *redis.Client
	orchestratorClient *circuitbreaker.OrchestratorClient
}

func NewPaymentHandler(repo interfaces.PaymentRepository, redisClient *redis.Client, orchestratorClient *circuitbreaker.OrchestratorClient) *PaymentHandler {
	return &PaymentHandler{
		repo:               repo,
		redisClient:        redisClient,
		orchestratorClient: orchestratorClient,
	}
}

func (h *PaymentHandler) CreatePayment(c *gin.Context) {
	ctx := c.Request.Context()
	span := trace.SpanFromContext(ctx)

	var req models.CreatePaymentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		telemetry.Logger.Warn("Invalid payment request", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	idempotencyKey := c.GetString("idempotency_key")

	payment := models.Payment{
		ID:             uuid.New().String(),
		Amount:         req.Amount,
		Currency:       req.Currency,
		CustomerID:     req.CustomerID,
		MerchantID:     req.MerchantID,
		Status:         "NEW",
		IdempotencyKey: idempotencyKey,
		CreatedAt:      time.Now(),
	}

	telemetry.Logger.Info("Creating payment",
		zap.String("payment_id", payment.ID),
		zap.String("customer_id", payment.CustomerID),
		zap.Float64("amount", payment.Amount),
		zap.String("trace_id", span.SpanContext().TraceID().String()),
	)

	// Save to database
	if err := h.repo.Create(ctx, &payment); err != nil {
		telemetry.Logger.Error("Failed to save payment to database",
			zap.String("payment_id", payment.ID),
			zap.Error(err),
		)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create payment"})
		return
	}

	// Cache in Redis
	paymentJSON, err := json.Marshal(payment)
	if err != nil {
		telemetry.Logger.Error("Failed to marshal payment", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
		return
	}

	if err := h.redisClient.Set(ctx, fmt.Sprintf("idempotency:%s", idempotencyKey), paymentJSON, 24*time.Hour).Err(); err != nil {
		telemetry.Logger.Warn("Failed to cache payment in Redis",
			zap.String("payment_id", payment.ID),
			zap.Error(err),
		)
		// Redis кэш не критичен, продолжаем
	}

	// Send to Payment Orchestrator via gRPC (with circuit breaker + retry)
	_, err = h.orchestratorClient.ProcessPayment(ctx, &paymentpb.ProcessPaymentRequest{
		PaymentId:  payment.ID,
		Amount:     payment.Amount,
		Currency:   payment.Currency,
		CustomerId: payment.CustomerID,
		MerchantId: payment.MerchantID,
		Status:     payment.Status,
		CreatedAt:  payment.CreatedAt.Format(time.RFC3339),
	})
	if err != nil {
		telemetry.Logger.Error("Failed to send payment to orchestrator via gRPC",
			zap.String("payment_id", payment.ID),
			zap.Error(err),
		)
		if strings.Contains(err.Error(), "circuit breaker open") {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Payment orchestrator is temporarily unavailable"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to process payment"})
		}
		return
	}

	telemetry.Logger.Info("Payment created successfully",
		zap.String("payment_id", payment.ID),
	)

	c.JSON(http.StatusCreated, payment)
}

func (h *PaymentHandler) GetPayment(c *gin.Context) {
	id := c.Param("id")

	payment, err := h.repo.GetByID(c.Request.Context(), id)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "Payment not found"})
		return
	}
	if err != nil {
		telemetry.Logger.Error("Failed to fetch payment from database",
			zap.String("payment_id", id),
			zap.Error(err),
		)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch payment"})
		return
	}

	c.JSON(http.StatusOK, payment)
}

func (h *PaymentHandler) ConfirmPayment(c *gin.Context) {
	id := c.Param("id")

	if err := h.repo.UpdateStatus(c.Request.Context(), id, "CONFIRMED"); err != nil {
		telemetry.Logger.Error("Failed to confirm payment",
			zap.String("payment_id", id),
			zap.Error(err),
		)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to confirm payment"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "confirmed", "payment_id": id})
}
