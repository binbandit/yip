# Setup

1. Start the gateway: `go run ./gateway`.
2. The queue producer publishes to the `beacon.requests` topic.
3. Retries are handled by the retry worker service (separate deployment).
