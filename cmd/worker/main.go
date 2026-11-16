package main

import (
	"log"
	"os"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"

	"temporalpayments/internal/payment"
)

func main() {
	logger := log.New(os.Stdout, "payment-worker ", log.LstdFlags|log.Lmicroseconds)
	temporalAddress := envOrDefault("TEMPORAL_ADDRESS", "localhost:7233")
	stubURL := envOrDefault("STUB_SERVICE_URL", "http://localhost:8081")

	temporalClient := connectWithRetry(logger, temporalAddress)
	defer temporalClient.Close()

	paymentWorker := worker.New(temporalClient, payment.TaskQueue, worker.Options{})
	activities := &payment.Activities{Services: payment.NewServiceClient(stubURL)}

	paymentWorker.RegisterWorkflow(payment.PaymentWorkflow)
	paymentWorker.RegisterActivityWithOptions(
		activities.AuthorizePayment,
		activity.RegisterOptions{Name: payment.AuthorizeActivity},
	)
	paymentWorker.RegisterActivityWithOptions(
		activities.LedgerPending,
		activity.RegisterOptions{Name: payment.LedgerPendingActivity},
	)
	paymentWorker.RegisterActivityWithOptions(
		activities.CapturePayment,
		activity.RegisterOptions{Name: payment.CaptureActivity},
	)
	paymentWorker.RegisterActivityWithOptions(
		activities.LedgerCompleted,
		activity.RegisterOptions{Name: payment.LedgerCompletedActivity},
	)
	paymentWorker.RegisterActivityWithOptions(
		activities.CompensatePayment,
		activity.RegisterOptions{Name: payment.CompensateActivity},
	)

	logger.Printf("polling task queue %q", payment.TaskQueue)
	if err := paymentWorker.Run(worker.InterruptCh()); err != nil {
		logger.Fatalf("run worker: %v", err)
	}
}

func connectWithRetry(logger *log.Logger, address string) client.Client {
	for {
		temporalClient, err := client.Dial(client.Options{HostPort: address})
		if err == nil {
			return temporalClient
		}
		logger.Printf("Temporal unavailable at %s: %v; retrying", address, err)
		time.Sleep(2 * time.Second)
	}
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
