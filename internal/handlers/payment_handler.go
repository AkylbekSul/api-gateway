package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/akylbek/payment-system/api-gateway/internal/interfaces"
	"github.com/akylbek/payment-system/api-gateway/internal/models"
	"github.com/akylbek/payment-system/api-gateway/internal/telemetry"
	paymentpb "github.com/akylbek/payment-system/proto/payment"
)

type PaymentHandler struct {
	repo               interfaces.PaymentRepository
	redisClient        *redis.Client
	orchestratorClient paymentpb.PaymentOrchestratorClient
}

func NewPaymentHandler(repo interfaces.PaymentRepository, redisClient *redis.Client, orchestratorClient paymentpb.PaymentOrchestratorClient) *PaymentHandler {
	return &PaymentHandler{
		repo:               repo,
		redisClient:        redisClient,
		orchestratorClient: orchestratorClient,
	}
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func (h *PaymentHandler) CreatePayment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	span := trace.SpanFromContext(ctx)

	var req models.CreatePaymentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		telemetry.Logger.Warn("Invalid payment request", zap.Error(err))
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	// Validate required fields
	if req.Amount == 0 || req.Currency == "" || req.CustomerID == "" || req.MerchantID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "amount, currency, customer_id, and merchant_id are required"})
		return
	}

	idempotencyKey, _ := ctx.Value(idempotencyKeyCtx).(string)

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
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to create payment"})
		return
	}

	// Cache in Redis
	paymentJSON, err := json.Marshal(payment)
	if err != nil {
		telemetry.Logger.Error("Failed to marshal payment", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Internal server error"})
		return
	}

	if err := h.redisClient.Set(ctx, fmt.Sprintf("idempotency:%s", idempotencyKey), paymentJSON, 24*time.Hour).Err(); err != nil {
		telemetry.Logger.Warn("Failed to cache payment in Redis",
			zap.String("payment_id", payment.ID),
			zap.Error(err),
		)
		// Redis cache is not critical, continue
	}

	// Send to Payment Orchestrator via gRPC
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
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to process payment"})
		return
	}

	telemetry.Logger.Info("Payment created successfully",
		zap.String("payment_id", payment.ID),
	)

	writeJSON(w, http.StatusCreated, payment)
}

func (h *PaymentHandler) GetPayment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	payment, err := h.repo.GetByID(r.Context(), id)
	if err == sql.ErrNoRows {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Payment not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to fetch payment"})
		return
	}

	writeJSON(w, http.StatusOK, payment)
}

func (h *PaymentHandler) ConfirmPayment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	if err := h.repo.UpdateStatus(r.Context(), id, "CONFIRMED"); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to confirm payment"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "confirmed", "payment_id": id})
}

// idempotencyKeyCtx is the context key for the idempotency key
type contextKey string

const idempotencyKeyCtx contextKey = "idempotency_key"
