package payment

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
)

func TestPaymentWorkflowCompletes(t *testing.T) {
	env := newTestEnvironment(t)
	mockSuccessfulSetup(env)
	env.OnActivity(CaptureActivity, mock.Anything, mock.Anything).
		Return(Capture{ID: "capture-1"}, nil).
		Once()
	env.OnActivity(LedgerCompletedActivity, mock.Anything, mock.MatchedBy(
		func(input LedgerCompletedInput) bool { return input.Status == "completed" },
	)).Return(nil).Once()

	env.ExecuteWorkflow(PaymentWorkflow, validRequest())

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var result PaymentResult
	require.NoError(t, env.GetWorkflowResult(&result))
	require.Equal(t, "completed", result.Status)
	require.Equal(t, "capture-1", result.CaptureID)
	env.AssertExpectations(t)
}

func TestPaymentWorkflowCompensatesAfterCaptureRetries(t *testing.T) {
	env := newTestEnvironment(t)
	mockSuccessfulSetup(env)
	env.OnActivity(CaptureActivity, mock.Anything, mock.Anything).
		Return(Capture{}, errors.New("provider unavailable")).
		Times(5)
	env.OnActivity(LedgerCompletedActivity, mock.Anything, mock.MatchedBy(
		func(input LedgerCompletedInput) bool { return input.Status == "failed" },
	)).Return(nil).Once()
	env.OnActivity(CompensateActivity, mock.Anything, mock.Anything).
		Return(nil).
		Once()

	env.ExecuteWorkflow(PaymentWorkflow, validRequest())

	require.NoError(t, env.GetWorkflowError())
	var result PaymentResult
	require.NoError(t, env.GetWorkflowResult(&result))
	require.Equal(t, "failed", result.Status)
	env.AssertExpectations(t)
}

func TestPaymentWorkflowTimesOutAndCompensates(t *testing.T) {
	env := newTestEnvironment(t)
	mockSuccessfulSetup(env)
	env.OnActivity(CaptureActivity, mock.Anything, mock.Anything).
		After(31*time.Second).
		Return(Capture{ID: "too-late"}, nil).
		Once()
	env.OnActivity(LedgerCompletedActivity, mock.Anything, mock.MatchedBy(
		func(input LedgerCompletedInput) bool { return input.Status == "failed" },
	)).Return(nil).Once()
	env.OnActivity(CompensateActivity, mock.Anything, mock.Anything).
		Return(nil).
		Once()

	env.ExecuteWorkflow(PaymentWorkflow, validRequest())

	require.NoError(t, env.GetWorkflowError())
	var result PaymentResult
	require.NoError(t, env.GetWorkflowResult(&result))
	require.Equal(t, "timed_out", result.Status)
	env.AssertExpectations(t)
}

func newTestEnvironment(t *testing.T) *testsuite.TestWorkflowEnvironment {
	t.Helper()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.SetTestTimeout(5 * time.Second)
	registerTestActivities(env)
	return env
}

func registerTestActivities(env *testsuite.TestWorkflowEnvironment) {
	env.RegisterActivityWithOptions(
		func(context.Context, AuthorizeInput) (Authorization, error) { return Authorization{}, nil },
		activity.RegisterOptions{Name: AuthorizeActivity},
	)
	env.RegisterActivityWithOptions(
		func(context.Context, LedgerPendingInput) (LedgerEntry, error) { return LedgerEntry{}, nil },
		activity.RegisterOptions{Name: LedgerPendingActivity},
	)
	env.RegisterActivityWithOptions(
		func(context.Context, CaptureInput) (Capture, error) { return Capture{}, nil },
		activity.RegisterOptions{Name: CaptureActivity},
	)
	env.RegisterActivityWithOptions(
		func(context.Context, LedgerCompletedInput) error { return nil },
		activity.RegisterOptions{Name: LedgerCompletedActivity},
	)
	env.RegisterActivityWithOptions(
		func(context.Context, CompensateInput) error { return nil },
		activity.RegisterOptions{Name: CompensateActivity},
	)
}

func mockSuccessfulSetup(env *testsuite.TestWorkflowEnvironment) {
	env.OnActivity(AuthorizeActivity, mock.Anything, mock.Anything).
		Return(Authorization{ID: "authorization-1"}, nil).
		Once()
	env.OnActivity(LedgerPendingActivity, mock.Anything, mock.Anything).
		Return(LedgerEntry{ID: "ledger-1"}, nil).
		Once()
}

func validRequest() PaymentRequest {
	return PaymentRequest{
		AmountMinor: 2500,
		Currency:    "USD",
		CustomerID:  "customer-123",
		Item:        "demo-item",
	}
}
