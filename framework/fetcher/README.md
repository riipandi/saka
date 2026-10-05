# Fetcher

Fetcher is saka's outbound HTTP client. Integrations call external services through one
`Client`; the composition root builds it from `config.Fetcher` and the process logger.

> **Relation to the registry:** `internal/registry` registers the client, `serve` resolves
> it before the listener opens, and the injector's shutdown closes its idle connections.
> A feature receives the client. It does not construct one.

## Features

- **One client for every integration** — method, absolute URL, query, headers, and
  body in, status, headers, and body out. The upstream address is hardcoded at
  the call site
- **A product token, not a browser token** — `saka/<version> (+https://github.com/riipandi/saka)`.
  The generated config file does not list `fetcher.user_agent`; the binary default stands
- **One attempt budget** — `fetcher.timeout` bounds the dial, the handshake, and the
  attempt. The caller's context still cancels the attempt and any wait between retries
- **Retries only for transient failures** — jittered exponential backoff, HTTP 408, 429,
  and 5xx except 501, plus network failures. Other 4xx are not retried. POST and PATCH
  are not retried
- **A circuit breaker per host** — one upstream opening does not stop calls to
  another. The failure threshold stays above the retry count, and the open
  period is at least one attempt
- **Errors a caller can match** — network, timeout, cancellation, retry exhaustion, an
  open circuit, and HTTP status (`errors.Is`). The text has no query string and no body
- **Logs without credentials** — success is a debug line with method, host, path, and
  status. Bodies are not logged. Resty's own lines are scrubbed before they reach slog
- **W3C trace headers** — each call is a client span, and `traceparent` is sent
  with it. A trace handed to this process is forwarded even when export is off

## Wiring

```go
client := do.MustInvoke[*fetcher.Client](injector)
res, err := client.Do(ctx, fetcher.Request{
    Method: http.MethodGet,
    URL:    "https://example.com/v1/items",
    Query:  url.Values{"page": {"1"}},
})
```

The URL is absolute `http` or `https`. A relative URL is refused: there is no
configured base to join it to.

## Defaults

| Key | Default |
| --- | --- |
| `fetcher.timeout` | 10s |
| `fetcher.retry_count` | 2 extra attempts |
| `fetcher.retry_wait` | 1s |
| `fetcher.retry_max_wait` | 8s |
| `fetcher.circuit_failure_threshold` | 5 |
| `fetcher.circuit_success_threshold` | 2 |
| `fetcher.circuit_reset_timeout` | 30s |
| `fetcher.max_body_bytes` | 16 MiB |

Durations in the config file are seconds. `retry_count` cannot exceed 5.
