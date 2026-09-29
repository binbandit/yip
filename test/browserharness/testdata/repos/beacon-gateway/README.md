# Beacon gateway

Accepts client requests, assigns a request ID, and hands work to the queue
producer. Failed deliveries are retried by the Beacon retry worker, which
lives in a separate repository.
