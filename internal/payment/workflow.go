package payment

import (
	"fmt"
	"strings"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const captureDeadline = 30 * time.Second

func PaymentWorkflow(ctx workflow.Context, request PaymentRequest) (PaymentResult, error) {
	if err := validateRequest(request); err != nil {
		return PaymentResult{}, temporal.NewNonRetryableApplicationError(
			err.Error(), "InvalidPaymentRequest", nil,
		)
	}

	paymentID := workflow.GetInfo(ctx).WorkflowExecution.ID
	result := PaymentResult{PaymentID: paymentID, Status: "started"}
	ctx = workflow.WithActivityOptions(ctx, activityOptions())

	var authorization Authorization
	err := workflow.ExecuteActivity(ctx, AuthorizeActivity, AuthorizeInput{
		PaymentID:      paymentID,
		IdempotencyKey: operationKey(paymentID, "authorize"),
		PaymentRequest: request,
	}).Get(ctx, &authorization)
	if err != nil {
		return result, fmt.Errorf("authorize payment: %w", err)
	}
	result.AuthorizationID = authorization.ID
	result.Status = "authorized"

	var ledger LedgerEntry
	err = workflow.ExecuteActivity(ctx, LedgerPendingActivity, LedgerPendingInput{
		PaymentID:       paymentID,
		AuthorizationID: authorization.ID,
		IdempotencyKey:  operationKey(paymentID, "ledger-pending"),
		PaymentRequest:  request,
	}).Get(ctx, &ledger)
	if err != nil {
		return result, fmt.Errorf("write pending ledger entry: %w", err)
	}
	result.LedgerEntryID = ledger.ID
	result.Status = "pending"

	capture, captureErr, timedOut := executeCapture(ctx, request, paymentID, authorization.ID)
	if captureErr != nil || timedOut {
		reason := "capture failed"
		status := "failed"
		if timedOut {
			reason = "banking provider did not respond within 30 seconds"
			status = "timed_out"
		} else {
			reason = captureErr.Error()
		}
		result.Status = status
		result.Failure = reason

		if err := failAndCompensate(ctx, result, reason); err != nil {
			return result, err
		}
		return result, nil
	}

	result.CaptureID = capture.ID
	err = workflow.ExecuteActivity(ctx, LedgerCompletedActivity, LedgerCompletedInput{
		PaymentID:      paymentID,
		LedgerEntryID:  ledger.ID,
		CaptureID:      capture.ID,
		Status:         "completed",
		IdempotencyKey: operationKey(paymentID, "ledger-completed"),
	}).Get(ctx, nil)
	if err != nil {
		return result, fmt.Errorf("mark ledger completed after successful capture: %w", err)
	}

	result.Status = "completed"
	return result, nil
}

func executeCapture(
	ctx workflow.Context,
	request PaymentRequest,
	paymentID string,
	authorizationID string,
) (Capture, error, bool) {
	activityCtx, cancelActivity := workflow.WithCancel(ctx)
	defer cancelActivity()
	activityCtx = workflow.WithActivityOptions(activityCtx, captureActivityOptions())

	timerCtx, cancelTimer := workflow.WithCancel(ctx)
	defer cancelTimer()

	var capture Capture
	var captureErr error
	captureFinished := false
	timedOut := false

	captureFuture := workflow.ExecuteActivity(activityCtx, CaptureActivity, CaptureInput{
		PaymentID:       paymentID,
		AuthorizationID: authorizationID,
		IdempotencyKey:  operationKey(paymentID, "capture"),
		PaymentRequest:  request,
	})
	timerFuture := workflow.NewTimer(timerCtx, captureDeadline)

	selector := workflow.NewSelector(ctx)
	selector.AddFuture(captureFuture, func(f workflow.Future) {
		captureFinished = true
		captureErr = f.Get(ctx, &capture)
		cancelTimer()
	})
	selector.AddFuture(timerFuture, func(f workflow.Future) {
		if f.Get(ctx, nil) == nil && !captureFinished {
			timedOut = true
			cancelActivity()
		}
	})
	selector.Select(ctx)

	if timedOut {
		// Wait for the activity cancellation to be acknowledged before compensating.
		_ = captureFuture.Get(ctx, nil)
	}
	return capture, captureErr, timedOut
}

func failAndCompensate(ctx workflow.Context, result PaymentResult, reason string) error {
	var ledgerErr error
	ledgerErr = workflow.ExecuteActivity(ctx, LedgerCompletedActivity, LedgerCompletedInput{
		PaymentID:      result.PaymentID,
		LedgerEntryID:  result.LedgerEntryID,
		Status:         "failed",
		Failure:        reason,
		IdempotencyKey: operationKey(result.PaymentID, "ledger-failed"),
	}).Get(ctx, nil)

	compensationErr := workflow.ExecuteActivity(ctx, CompensateActivity, CompensateInput{
		PaymentID:       result.PaymentID,
		AuthorizationID: result.AuthorizationID,
		Reason:          reason,
		IdempotencyKey:  operationKey(result.PaymentID, "release"),
	}).Get(ctx, nil)

	if ledgerErr != nil || compensationErr != nil {
		return fmt.Errorf("capture failed; ledger failure update: %v; compensation: %v", ledgerErr, compensationErr)
	}
	return nil
}

func activityOptions() workflow.ActivityOptions {
	return workflow.ActivityOptions{
		StartToCloseTimeout: 15 * time.Second,
		RetryPolicy:         retryPolicy(),
	}
}

func captureActivityOptions() workflow.ActivityOptions {
	return workflow.ActivityOptions{
		StartToCloseTimeout: 45 * time.Second,
		HeartbeatTimeout:    3 * time.Second,
		WaitForCancellation: true,
		RetryPolicy:         retryPolicy(),
	}
}

func retryPolicy() *temporal.RetryPolicy {
	return &temporal.RetryPolicy{
		InitialInterval:    time.Second,
		BackoffCoefficient: 2,
		MaximumInterval:    8 * time.Second,
		MaximumAttempts:    5,
	}
}

func operationKey(paymentID, operation string) string {
	return paymentID + ":" + operation
}

func validateRequest(request PaymentRequest) error {
	switch {
	case request.AmountMinor <= 0:
		return fmt.Errorf("amountMinor must be greater than zero")
	case strings.TrimSpace(request.Currency) == "":
		return fmt.Errorf("currency is required")
	case strings.TrimSpace(request.CustomerID) == "":
		return fmt.Errorf("customerId is required")
	case strings.TrimSpace(request.Item) == "":
		return fmt.Errorf("item is required")
	default:
		return nil
	}
}
