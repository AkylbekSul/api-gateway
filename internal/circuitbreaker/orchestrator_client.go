package circuitbreaker

import (
	"context"
	"fmt"
	"time"

	"github.com/sony/gobreaker/v2"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/akylbek/payment-system/api-gateway/internal/telemetry"
	paymentpb "github.com/akylbek/payment-system/proto/payment"
)

// OrchestratorClient wraps the gRPC client with circuit breaker, timeout, and retry logic.
type OrchestratorClient struct {
	client     paymentpb.PaymentOrchestratorClient
	cb         *gobreaker.CircuitBreaker[*paymentpb.ProcessPaymentResponse]
	cbGet      *gobreaker.CircuitBreaker[*paymentpb.GetPaymentStateResponse]
	timeout    time.Duration
	maxRetries int
}

// NewOrchestratorClient creates a new circuit-breaker-wrapped orchestrator client.
func NewOrchestratorClient(conn *grpc.ClientConn, timeout time.Duration, maxRetries int) *OrchestratorClient {
	client := paymentpb.NewPaymentOrchestratorClient(conn)

	cbSettings := gobreaker.Settings{
		Name:        "orchestrator-process-payment",
		MaxRequests: 3,                // allow 3 requests in half-open state
		Interval:    10 * time.Second, // cyclic period of closed state to clear counts
		Timeout:     15 * time.Second, // time to wait before switching from open to half-open
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			failureRatio := float64(counts.TotalFailures) / float64(counts.Requests)
			return counts.Requests >= 5 && failureRatio >= 0.6
		},
		OnStateChange: func(name string, from gobreaker.State, to gobreaker.State) {
			telemetry.Logger.Warn("Circuit breaker state change",
				zap.String("name", name),
				zap.String("from", from.String()),
				zap.String("to", to.String()),
			)
		},
	}

	cbGetSettings := cbSettings
	cbGetSettings.Name = "orchestrator-get-payment-state"

	return &OrchestratorClient{
		client:     client,
		cb:         gobreaker.NewCircuitBreaker[*paymentpb.ProcessPaymentResponse](cbSettings),
		cbGet:      gobreaker.NewCircuitBreaker[*paymentpb.GetPaymentStateResponse](cbGetSettings),
		timeout:    timeout,
		maxRetries: maxRetries,
	}
}

// ProcessPayment sends a payment processing request with circuit breaker, timeout, and retry.
func (o *OrchestratorClient) ProcessPayment(ctx context.Context, req *paymentpb.ProcessPaymentRequest) (*paymentpb.ProcessPaymentResponse, error) {
	resp, err := o.cb.Execute(func() (*paymentpb.ProcessPaymentResponse, error) {
		return o.processPaymentWithRetry(ctx, req)
	})
	if err != nil {
		if err == gobreaker.ErrOpenState {
			telemetry.Logger.Error("Circuit breaker is open, rejecting ProcessPayment request",
				zap.String("payment_id", req.PaymentId),
			)
			return nil, fmt.Errorf("service unavailable: circuit breaker open")
		}
		return nil, err
	}
	return resp, nil
}

// GetPaymentState retrieves payment state with circuit breaker and timeout.
func (o *OrchestratorClient) GetPaymentState(ctx context.Context, req *paymentpb.GetPaymentStateRequest) (*paymentpb.GetPaymentStateResponse, error) {
	resp, err := o.cbGet.Execute(func() (*paymentpb.GetPaymentStateResponse, error) {
		callCtx, cancel := context.WithTimeout(ctx, o.timeout)
		defer cancel()

		return o.client.GetPaymentState(callCtx, req)
	})
	if err != nil {
		if err == gobreaker.ErrOpenState {
			telemetry.Logger.Error("Circuit breaker is open, rejecting GetPaymentState request",
				zap.String("payment_id", req.PaymentId),
			)
			return nil, fmt.Errorf("service unavailable: circuit breaker open")
		}
		return nil, err
	}
	return resp, nil
}

func (o *OrchestratorClient) processPaymentWithRetry(ctx context.Context, req *paymentpb.ProcessPaymentRequest) (*paymentpb.ProcessPaymentResponse, error) {
	var lastErr error

	for attempt := 0; attempt <= o.maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(attempt*100) * time.Millisecond
			telemetry.Logger.Warn("Retrying ProcessPayment gRPC call",
				zap.String("payment_id", req.PaymentId),
				zap.Int("attempt", attempt),
				zap.Duration("backoff", backoff),
			)
			time.Sleep(backoff)
		}

		callCtx, cancel := context.WithTimeout(ctx, o.timeout)
		resp, err := o.client.ProcessPayment(callCtx, req)
		cancel()

		if err == nil {
			return resp, nil
		}

		lastErr = err

		// Only retry on transient errors (Unavailable, DeadlineExceeded, ResourceExhausted)
		st, ok := status.FromError(err)
		if !ok || !isRetryable(st.Code()) {
			telemetry.Logger.Error("Non-retryable gRPC error in ProcessPayment",
				zap.String("payment_id", req.PaymentId),
				zap.Error(err),
			)
			return nil, err
		}

		telemetry.Logger.Warn("Retryable gRPC error in ProcessPayment",
			zap.String("payment_id", req.PaymentId),
			zap.String("grpc_code", st.Code().String()),
			zap.Error(err),
		)
	}

	telemetry.Logger.Error("All retries exhausted for ProcessPayment",
		zap.String("payment_id", req.PaymentId),
		zap.Int("max_retries", o.maxRetries),
		zap.Error(lastErr),
	)

	return nil, fmt.Errorf("all retries exhausted: %w", lastErr)
}

func isRetryable(code codes.Code) bool {
	switch code {
	case codes.Unavailable, codes.DeadlineExceeded, codes.ResourceExhausted:
		return true
	default:
		return false
	}
}
