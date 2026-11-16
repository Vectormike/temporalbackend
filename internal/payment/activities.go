package payment

import (
	"context"
	"time"

	"go.temporal.io/sdk/activity"
)

type Activities struct {
	Services *ServiceClient
}

func (a *Activities) AuthorizePayment(ctx context.Context, input AuthorizeInput) (Authorization, error) {
	var result Authorization
	err := a.Services.Post(ctx, "/authorize", input.IdempotencyKey, input, &result)
	return result, err
}

func (a *Activities) LedgerPending(ctx context.Context, input LedgerPendingInput) (LedgerEntry, error) {
	var result LedgerEntry
	err := a.Services.Post(ctx, "/ledger/pending", input.IdempotencyKey, input, &result)
	return result, err
}

func (a *Activities) CapturePayment(ctx context.Context, input CaptureInput) (Capture, error) {
	stopHeartbeat := make(chan struct{})
	defer close(stopHeartbeat)

	activity.RecordHeartbeat(ctx, "calling banking provider")
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				activity.RecordHeartbeat(ctx, "waiting for banking provider")
			case <-stopHeartbeat:
				return
			case <-ctx.Done():
				return
			}
		}
	}()

	var result Capture
	err := a.Services.Post(ctx, "/capture", input.IdempotencyKey, input, &result)
	return result, err
}

func (a *Activities) LedgerCompleted(ctx context.Context, input LedgerCompletedInput) error {
	return a.Services.Post(ctx, "/ledger/completed", input.IdempotencyKey, input, nil)
}

func (a *Activities) CompensatePayment(ctx context.Context, input CompensateInput) error {
	return a.Services.Post(ctx, "/release", input.IdempotencyKey, input, nil)
}
