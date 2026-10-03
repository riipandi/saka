# API Response

Two transports, one contract: ConnectRPC serves the internal backoffice and
is the primary protocol; REST serves external integrations, and the
OAuth2/OIDC identity-provider features will land on it. The contract the two
share is semantic — field naming, the pagination rules, the error semantics —
and each transport writes it in its own idiom. The rules are defined once in
`pkg/responder` and both transports use them, so a client reads the same
page, limit, totals, and item range from either surface.

## Field naming

Every JSON field on both transports is **snake_case**.

REST gets this from the Go struct tags. ConnectRPC needs an explicit
step: protobuf's JSON mapping defaults to lowerCamelCase, so each
service registers a codec that serializes under the declared proto
field names instead. Without it the two transports of one contract
would disagree on field naming.

Requests are tolerant: protojson accepts both spellings, so a body may
send `display_name` or `displayName` and unmarshal the same way.

Three protocol surfaces keep camelCase because their specifications
require it, and none is part of this envelope contract:

- SCIM 2.0 (`/scim/v2/*`) — `userName`, `displayName`, `givenName`, `Resources`, `totalResults`.
- WebAuthn (`/api/webauthn/*`) — `publicKey`, `rp`, `user`, `challenge`.
- OIDC (planned) — its endpoints answer the shapes the specification
  defines, not the envelope.

One field stays camelCase inside an otherwise snake_case body, and it is
connect's, not ours: the `debug` echo connect attaches to each entry of a
failure body's `details`. Connect renders it with protobuf's default JSON
mapping, so it reads `ruleId`, `fieldNumber`. The authority beside it is
`details[].value` — the base64 detail itself, whose fields are the proto
ones (`rule_id`, `field`, `message`) and whose field paths name the proto
field (`user_id`). `debug` is a reading aid, not a contract field: parse
`value`.

## Where each fact lives

| Fact                | ConnectRPC (internal, primary)                        | REST (external)                          |
| ------------------- | ----------------------------------------------------- | ---------------------------------------- |
| Status              | the flat `status` field (`"success"`); a failure never reaches a body — it is the connect code | `status` + `metadata.status_code`        |
| Message             | the flat `message` field — the sentence a UI shows as-is | `message` (success and error alike)   |
| Error message       | the connect error message (the failure's whole answer)| `message` in the error envelope          |
| Structured error    | a typed message attached as a connect error detail    | `error` in the error envelope            |
| Request/trace id    | the `X-Request-Id` response header                    | `metadata.request_id` + the same header  |
| Rate limit          | `X-RateLimit-*` headers; a limited call is refused with `resource_exhausted` (429) | `metadata.rate_limit` + the same headers; a limited request is refused with the envelope |
| Pagination          | a `saka.common.v1.ListMetadata` block on the message | `metadata` pagination fields             |
| Payload             | the response message's own typed fields               | `data` in the envelope                   |
| Links (HATEOAS)     | not modelled; a page token when a list needs one      | the `links` map                          |

The outcome pair is the one envelope piece both transports write into the
body — two flat fields, the same keys on either surface: a client checks
`status` and reads `message` the same way on either transport. What stays out of the body is what the protocol already carries
better — the correlation id, the rate-limit window, and on RPC the whole
failure half: a failed call answers a connect error (code and message), so
a success body's `status` is always `"success"`. The correlation and
rate-limit facts are per-request, and the RPC response body carries none of
them — that is why `common.proto` once carried `status_code` and
`request_id` and carries them no more; the pagination block and the outcome
block are the shared pieces that survive.

## REST envelope

Minimal success response:

```json
{
    status: success
    message?: string
    metadata: {
        status_code: number (http code)
        request_id: string
    }
}
```

Complete success response:

```json
{
    status: success
    message?: string
    data?: {} | []
    metadata: {
        status_code: number (http code)
        request_id: string
        trace_id?: string

        // Rate limit information
        rate_limit?: ApiRateLimit

        // Pagination information (HATEOAS-first)
        ...ApiPagination
    }

    // Simple HATEOAS-friendly link map.
    // Keys are link relations (e.g. "self", "next", "prev", "related") and values are URLs or null.
    links? Record<string, string | null> | null
}
```

ApiPagination:

```json
{
    page?: number | null // current page number
    limit?: number | null // number of items per page

    // Totals (optional when unknown or not applicable)
    total_pages?: number | null
    total_items?: number | null

    // Inclusive item range in the current page (optional)
    first_item_index?: number | null
    last_item_index?: number | null
}
```

PaginationParams (query params):

```json
{
    page?: number // number of the page to retrieve, set -1 for all records
    limit?: number // number of items per page, set -1 for all records
    sort_by?: string // sort by column or field name
    sort_order?: 'asc' | 'desc' // sort direction
}
```

Pagination rules (enforced once in `pkg/responder`, shared by REST and
ConnectRPC):

- `page` starts at 1; a value below 1 becomes 1.
- `limit` defaults to 25 and caps at 100.
- `-1` in either field returns every record.
- On the REST query surface an empty `page`/`limit` takes the default; a
  present but invalid value is rejected. On the ConnectRPC surface a proto3
  `int32` cannot express "unset" separately from zero, so `0` takes the
  default rather than returning an unbounded result.
- `first_item_index` and `last_item_index` are zero-based and inclusive.
  Both are omitted when the range is unknown (empty result, or every record
  requested with no rows).
- `sort_by`/`sort_order` are parsed by `ParsePagination` but no store applies
  them yet; each list keeps its own fixed order.

Error response:

```json
{
    status: error
    message: string (general error message)
    error?: {} | [] | null
    metadata: {
        status_code: number (http code)
        request_id: string
    }
}
```

Links:

```json
{
    links?: {
        self?: string | null
        next?: string | null
        prev?: string | null
        first?: string | null
        last?: string | null
        [rel: string]: string | null | undefined
    } | null
}
```

ApiRateLimit:

```json
{
    limit: number // maximum number of requests allowed in the current window
    remaining: number // number of requests left in the current window
    reset: number // unix timestamp when the limit resets
}
```

## ConnectRPC surface

A procedure answers its response message alone: typed payload fields, and the
`saka.common.v1.ListMetadata` block on a list message. The facts around the
payload travel with the protocol, and a generated client reads them without
parsing a body:

- **Status** is the connect code: `ok` on success, `unavailable` for a
  dependency that is down, `unimplemented` for an unknown procedure.
- **The correlation id** is the `X-Request-Id` response header the request
  middleware writes on every answer — success, failure, and the not-found
  boundary — before any handler runs. No handler or interceptor writes it
  again: connect merges handler-set headers by appending, so a second writer
  answers with the id twice.
- **Rate-limit state** is the `X-RateLimit-*` response headers the limiter
  middleware writes. A limited call is refused with `resource_exhausted` —
  the code the specification maps to 429 — and a `Retry-After` header, never
  the REST envelope. The health procedure is not throttled, the same as its
  REST twin.
- **A structured failure** is a message attached to the connect error's
  details, the typed counterpart of the REST envelope's `error` field.
- **Request validation** is declarative: the constraints live in the
  contracts as protovalidate options (`buf.validate.field`), and the validate
  interceptor enforces them before a handler runs. A refusal is
  `invalid_argument` with a `buf.validate.ErrorInfo` detail — the field
  paths and rule descriptions a client renders per field. A rule a contract
  cannot express (a uniqueness check, a token's existence) is domain logic
  and answers its own code from the feature; the REST surface keeps
  `pkg/validate`'s field errors in the envelope.

## Authorization

Authorization is declared per procedure and per route in `internal/guard`, and
enforced by one guard interceptor on the RPC surface and one middleware on
REST. A request the tables do not name is **administrative**, so a new
endpoint is protected by default.

The refusal a client reads is deliberately not `permission_denied`:

| Situation | Code | REST status | Body |
| --- | --- | --- | --- |
| No credential | `unauthenticated` | 401 | `authentication required` |
| Credential, wrong role | `not_found` | 404 | `not found` |
| Credential, another account's resource | `not_found` | 404 | `not found` |
| Impersonated caller on a self-service request | `not_found` | 404 | `not found` |

`not_found` is the shape that discloses least, and the choice is a rule rather
than a convenience: a caller who may not act on an account learns nothing about
whether it exists, and a caller without the role cannot tell an administrative
procedure from an absent one. Because authorization runs **before** request
validation, the answer does not change with the body either — a malformed
request to a procedure the caller may not run reads exactly like a request to a
procedure that does not exist. The distinction between "not yours", "not
allowed", and "not there" survives in the audit record and the server log,
which an operator reads.

`Self` compares the account the request names against the caller's token
subject, and an administrator does not pass by virtue of the role: the
administrative procedures are where an administrator acts on someone else,
which is how upstream Pocket ID expresses the same rule (`/users/me` beside
`/users/{id}`). A **delegated (impersonated) caller** is refused on every
`Self` request even when the subject matches — a delegation reaches the
administrative surface, never the requests that belong to the account.
