package payment

const (
	TaskQueue = "payments"

	AuthorizeActivity       = "authorizePayment"
	LedgerPendingActivity   = "ledgerPending"
	CaptureActivity         = "capturePayment"
	LedgerCompletedActivity = "ledgerCompleted"
	CompensateActivity      = "compensatePayment"
)

type PaymentRequest struct {
	AmountMinor int64  `json:"amountMinor"`
	Currency    string `json:"currency"`
	CustomerID  string `json:"customerId"`
	Item        string `json:"item"`
}

type PaymentResult struct {
	PaymentID       string `json:"paymentId"`
	Status          string `json:"status"`
	AuthorizationID string `json:"authorizationId,omitempty"`
	LedgerEntryID   string `json:"ledgerEntryId,omitempty"`
	CaptureID       string `json:"captureId,omitempty"`
	Failure         string `json:"failure,omitempty"`
}

type AuthorizeInput struct {
	PaymentID      string `json:"paymentId"`
	IdempotencyKey string `json:"idempotencyKey"`
	PaymentRequest
}

type Authorization struct {
	ID string `json:"id"`
}

type LedgerPendingInput struct {
	PaymentID       string `json:"paymentId"`
	AuthorizationID string `json:"authorizationId"`
	IdempotencyKey  string `json:"idempotencyKey"`
	PaymentRequest
}

type LedgerEntry struct {
	ID string `json:"id"`
}

type CaptureInput struct {
	PaymentID       string `json:"paymentId"`
	AuthorizationID string `json:"authorizationId"`
	IdempotencyKey  string `json:"idempotencyKey"`
	PaymentRequest
}

type Capture struct {
	ID string `json:"id"`
}

type LedgerCompletedInput struct {
	PaymentID      string `json:"paymentId"`
	LedgerEntryID  string `json:"ledgerEntryId"`
	CaptureID      string `json:"captureId,omitempty"`
	Status         string `json:"status"`
	Failure        string `json:"failure,omitempty"`
	IdempotencyKey string `json:"idempotencyKey"`
}

type CompensateInput struct {
	PaymentID       string `json:"paymentId"`
	AuthorizationID string `json:"authorizationId"`
	Reason          string `json:"reason"`
	IdempotencyKey  string `json:"idempotencyKey"`
}
