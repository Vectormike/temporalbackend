# Temporal payment workflow

A runnable payment workflow built with Go and the official
[`go.temporal.io/sdk`](https://pkg.go.dev/go.temporal.io/sdk). The local stack
contains one Temporal server, one worker process, Temporal UI, PostgreSQL for
Temporal, and an HTTP service that stands in for the internal payment services,
ledger, and banking provider.

Money is represented in minor units. For example, `2500 USD` means `$25.00`.

## Payment flow

`PaymentWorkflow` performs these operations in order:

1. `authorizePayment` reserves funds.
2. `ledgerPending` creates a pending ledger entry.
3. `capturePayment` asks the banking provider to move the money.
4. `ledgerCompleted` marks the entry completed.

Capture races a durable 30-second Temporal timer. If all capture attempts fail,
or the timer fires first, the workflow marks the ledger entry failed and runs
`compensatePayment` to release the authorization.

Every activity uses exponential retry backoff with at most five attempts. Every
HTTP call carries an operation-specific idempotency key derived from the
Temporal workflow ID, such as `payment-abc:capture`.

## Why a worker crash does not double-charge

Temporal records workflow decisions and completed activity results in event
history. A replacement worker replays that history deterministically and does
not execute activities whose completion is already recorded.

An activity can still complete its external side effect immediately before a
worker crash, without Temporal receiving its completion event. Temporal may
retry that activity. For this reason, event history alone is not an exactly-once
guarantee. The stub service persists responses by idempotency key on the
`stub-state` Docker volume. A retried external request gets the original response
instead of performing the operation again. Production services must provide the
same idempotency guarantee.

## Files

- `internal/payment/types.go`: workflow inputs, results, and activity contracts.
- `internal/payment/workflow.go`: durable sequence, retries, capture timer, and
  compensation.
- `internal/payment/activities.go`: activity implementations.
- `internal/payment/client.go`: HTTP client that attaches idempotency keys.
- `internal/stubservice/`: persistent, deduplicating external-service stub.
- `cmd/worker/main.go`: Temporal worker registration.
- `cmd/start-payment/main.go`: command-line workflow starter.
- `cmd/stub-service/main.go`: stub HTTP server.
- `internal/payment/workflow_test.go`: success, retry exhaustion, and timeout
  workflow tests.
- `Dockerfile`: builds all three Go executables.
- `docker-compose.yml`: local Temporal and application services.

## Run locally

Prerequisites: Docker with Compose.

Start the stack:

```sh
docker compose up --build -d
```

Wait until the worker is polling:

```sh
docker compose logs -f worker
```

Press `Ctrl-C` after the polling message; the containers continue running.
Temporal UI is available at [http://localhost:8080](http://localhost:8080).

Start a test payment:

```sh
docker compose run --rm start-payment \
  --amount 2500 \
  --currency USD \
  --customer customer-123 \
  --item demo-item
```

The command waits for completion and prints the workflow result as JSON.

## Exercise failure and timeout paths

A successful payment runs authorize, ledger pending, capture, and ledger
completed, then finishes with status `completed`. The runs below show what
happens when the banking provider does not capture the payment.

Each `docker compose run --rm start-payment` generates a new workflow ID. The
log line `started workflow_id=...` is the ID to open in the Temporal UI under
the `default` namespace:
[http://localhost:8080/namespaces/default/workflows](http://localhost:8080/namespaces/default/workflows).

Configure capture to fail, then start a payment. `env` sets the variable for
that command in both fish and bash. `--no-deps` keeps Compose from recreating
the stub with the default success mode when the payment starts:

```sh
env CAPTURE_MODE=fail docker compose up -d --force-recreate --no-deps stub-service
env CAPTURE_MODE=fail docker compose run --rm --no-deps start-payment
```

The printed result has `"status": "failed"`. In the Temporal UI the workflow
row stays Completed, because compensation finished and the workflow returned
successfully. Open the workflow: the result status is `failed`, the capture
activity is attempted five times, then the ledger entry is marked failed and
`compensatePayment` releases the authorization.

Configure the banking provider to respond after the 30-second workflow deadline:

```sh
env CAPTURE_MODE=timeout CAPTURE_DELAY=35s \
  docker compose up -d --force-recreate --no-deps stub-service
env CAPTURE_MODE=timeout CAPTURE_DELAY=35s \
  docker compose run --rm --no-deps start-payment
```

The printed result has `"status": "timed_out"`, and the same compensation steps
run. The workflow row in the UI stays Completed. This command waits the real
30 seconds. The Go tests advance workflow time automatically, so they do not.

Restore normal capture behavior:

```sh
env CAPTURE_MODE=success docker compose up -d --force-recreate --no-deps stub-service
```

The persisted stub responses can be inspected in the service logs:

```sh
docker compose logs stub-service
```

`replayed=true` means a request reused a previously stored idempotent response.

## Run tests

With Go 1.23 or later:

```sh
go test ./...
```

The Temporal test environment advances workflow time automatically, so the
30-second timeout test does not need to wait 30 real seconds.

## Stop and reset

Stop containers while retaining Temporal and stub data:

```sh
docker compose down
```

Delete all local data, including idempotency records:

```sh
docker compose down -v
```
