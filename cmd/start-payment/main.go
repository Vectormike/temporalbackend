package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"

	"go.temporal.io/sdk/client"

	"temporalpayments/internal/payment"
)

func main() {
	amountMinor := flag.Int64("amount", 2500, "payment amount in minor currency units")
	currency := flag.String("currency", "USD", "ISO currency code")
	customerID := flag.String("customer", "customer-123", "customer identifier")
	item := flag.String("item", "demo-item", "item description")
	paymentID := flag.String("payment-id", "", "optional stable workflow/payment ID")
	flag.Parse()

	if *paymentID == "" {
		*paymentID = "payment-" + randomID()
	}

	temporalClient, err := client.Dial(client.Options{
		HostPort: envOrDefault("TEMPORAL_ADDRESS", "localhost:7233"),
	})
	if err != nil {
		log.Fatalf("connect to Temporal: %v", err)
	}
	defer temporalClient.Close()

	request := payment.PaymentRequest{
		AmountMinor: *amountMinor,
		Currency:    *currency,
		CustomerID:  *customerID,
		Item:        *item,
	}
	run, err := temporalClient.ExecuteWorkflow(
		context.Background(),
		client.StartWorkflowOptions{
			ID:        *paymentID,
			TaskQueue: payment.TaskQueue,
		},
		payment.PaymentWorkflow,
		request,
	)
	if err != nil {
		log.Fatalf("start workflow: %v", err)
	}
	log.Printf("started workflow_id=%s run_id=%s", run.GetID(), run.GetRunID())

	var result payment.PaymentResult
	if err := run.Get(context.Background(), &result); err != nil {
		log.Fatalf("workflow failed: %v", err)
	}
	output, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		log.Fatalf("encode result: %v", err)
	}
	fmt.Println(string(output))
}

func randomID() string {
	value := make([]byte, 8)
	if _, err := rand.Read(value); err != nil {
		log.Fatalf("generate payment ID: %v", err)
	}
	return hex.EncodeToString(value)
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
