# aapi-codegen

AsyncAPI 3.x → Go code generator. The AsyncAPI counterpart to `oapi-codegen` (same idea, different spec). Builds on `go-jsonschema` (imported as a library) for the JSON Schema → Go pass.

Generates, from one `*.source.asyncapi.yaml`, one Go package containing:

- **Payload types** for every message (via `go-jsonschema`).
- **Typed Publisher** with `Send<MessageName>(ctx, ...params, msg, opts ...SendOption) error` per `action: send` operation (v0.1+).
- **Typed Subscriber** with `<MessageName>Handler` interface + `Subscribe<MessageName>(ctx, ...params, handler) error` per `action: receive` operation (v0.2+).
- **`PublishProperties`** + `SendOption` (`WithPriority`, `WithExpirationMillis`, …) honoring message/operation AMQP bindings (v0.3+).
- **Typed channel parameters** — enums become `type X string` + const block (v0.4); pattern-validated parameters become `type X string` + `NewX`/`MustX` constructors that regex-check input (v0.4.1).

## Usage

Minimal:

```shell
aapi-codegen SPEC.asyncapi.yaml
```

Defaults: `-o ./types.gen.go`, package derived from the output directory's basename (with the `v1`/`v2` version-folder rule prepending the parent — `lib/schemas/job-message/v1` → `jobmessagev1`).

With overrides:

```shell
aapi-codegen -package PKG -o OUT.go SPEC.asyncapi.yaml
aapi-codegen -config aapi-codegen.config.yaml SPEC.asyncapi.yaml   # rare
```

## Spec extension — `x-aapi-codegen`

Cross-tree `$ref` → import mappings live inside the AsyncAPI spec, colocated with the contract:

```yaml
x-aapi-codegen:
  schema-packages:
    - id: https://schemas.example.com/common/v1/header.schema.json
      package: example.com/lib/schemas/common/v1
      alias: commonv1
```

`$id` matches a schema's `$id` keyword. When a payload `$ref`s into a schema with that `$id`, the generated Go imports `commonv1` instead of inlining the type.

## Inline payloads + `components/schemas`

Payloads can be declared inline in the spec — no separate schema file needed:

```yaml
components:
  schemas:
    Tag:
      type: object
      required: [key, value]
      properties: { key: {type: string}, value: {type: string} }

channels:
  inlineDispatch:
    messages:
      InlineMessage:
        payload:
          type: object
          required: [id, tags]
          properties:
            id: { type: string }
            tags: { type: array, items: { $ref: '#/components/schemas/Tag' } }
```

Payload Go type name defaults to the message key (`InlineMessage`); an inline `title` overrides. `components/schemas` types are generated once and shared.

Shared message wrappers work the same way via `components.messages`: define the envelope once, reference it via `#/components/messages/X` from any number of channels. Multiple references dedupe to one generated Go payload type.

## Validation

By default, aapi-codegen emits `UnmarshalJSON` methods that enforce the JSON Schema constraints in the spec: `required` fields, `additionalProperties: false`, `format` keywords (uuid, email, uri, hostname, regex), `enum`, numeric and string bounds, etc. Invalid JSON is rejected at `json.Unmarshal` time with an error naming the violation.

Opt out per-spec when you need to hand-write your own `UnmarshalJSON` (e.g. to accept a legacy PascalCase wire format alongside the canonical camelCase — two `UnmarshalJSON` methods on the same type would be a compile error):

```yaml
# inside the spec
x-aapi-codegen:
  omit-validation: true
```

Or via the optional config file:

```yaml
# aapi-codegen.config.yaml
omit-validation: true
```

Either source opts out; neither overrides the other's opt-out.

## Build / development

Requires Go 1.26+. The `go.mod` directive sets the floor; CI tracks the version pinned in [`.github/versions.env`](.github/versions.env).

```shell
go test ./...
```

The build is a single Go binary. The patched `go-jsonschema` fork is wired in via local `replace` in [go.mod](go.mod) pointing at a sibling `../go-jsonschema` checkout — clone both repos as siblings under one root.

PRs run `go vet`, `go test -race`, `golangci-lint`, and a goreleaser snapshot build via [`.github/workflows/development.yaml`](.github/workflows/development.yaml). Tag pushes (`vX.Y.Z` and `vX.Y.Z-rc.N`) trigger the release workflow.

For distribution to downstream consumers, see [examples/use-aapi-codegen.sh](examples/use-aapi-codegen.sh) — a download-and-cache script modelled on `plheide/go-jsonschema`'s `use-go-jsonschema.sh`. Pinning the binary version this way avoids the `go install`/replace-directive incompatibility. Supports Linux, macOS, and Windows (via Git Bash / WSL).

### Examples

Worked specs and assertions live under [`internal/test/`](internal/test/):

| Fixture | Exercises |
| --- | --- |
| [`widgetservice/`](internal/test/widgetservice/) | direct exchange, templated and literal addresses, cross-tree `x-aapi-codegen.schema-packages` import |
| [`notificationservice/`](internal/test/notificationservice/) | topic exchange, single-parameter address |
| [`inlineservice/`](internal/test/inlineservice/) | inline payloads + `components.schemas` |
| [`sharedmsgservice/`](internal/test/sharedmsgservice/) | `components.messages` referenced from multiple channels |
| [`messagecollisionservice/`](internal/test/messagecollisionservice/) | regression for inline + component-message keys colliding |
| [`validationservice/`](internal/test/validationservice/) | `omit-validation` opt-out |
| [`consumerservice/`](internal/test/consumerservice/) | **v0.2** — `action: receive`, `<Msg>Handler` interface, `Subscribe<Msg>`, `ErrDrop` semantics, queue-mode channels |
| [`bindingsservice/`](internal/test/bindingsservice/) | **v0.3** — `SendOption` + message/operation AMQP bindings (priority, expiration, contentEncoding, messageType) |
| [`enumparams/`](internal/test/enumparams/) | **v0.4** — typed channel parameters from `schema.type: string + enum`, dedup across publisher + subscriber |
| [`validatedparams/`](internal/test/validatedparams/) | **v0.4.1** — pattern-validated parameters with `NewX`/`MustX` constructors; `omit-validation` falls back to plain `string` |

## Status

aapi-codegen covers both publisher and subscriber AMQP code generation from AsyncAPI 3.x specs. Other protocol bindings (Kafka, WebSocket) are the main open scope.

### AsyncAPI 3.x ([spec](https://www.asyncapi.com/docs/reference/specification/v3.1.0))

- [x] `asyncapi: 3.x` version detection
- [x] `info.title` (used in generated publisher/subscriber doc comment)
- [x] `channels`
  - [x] `address` — literal (e.g. `widget.cancellation`)
  - [x] `address` — templated, multi-parameter (e.g. `{tenant}.{widgetType}`)
  - [x] `address` — templated, single-parameter (e.g. `{workflowName}`)
  - [x] `parameters` — `string` by default; **v0.4** types from `schema.type: string + enum`; **v0.4.1** types from `schema.type: string + pattern` with `NewX`/`MustX` constructors
  - [x] `messages` — multi-message channels
  - [x] `bindings.amqp` — typed per channel
- [x] `operations`
  - [x] `action: send` — emits typed `Publisher.Send<MessageName>(ctx, ...params, msg, opts ...SendOption) error` methods
  - [x] `action: receive` — emits `<MessageName>Handler` interface + `Subscriber.Subscribe<MessageName>(ctx, ...params, handler) error` (**v0.2**)
  - [x] `operations.X.channel.$ref` — internal ref resolution
  - [x] `operations.X.messages[].$ref` — internal ref resolution (exactly one message per operation)
- [x] `components.schemas` — shared types declared once, referenced from multiple payloads via `#/components/schemas/X`
- [x] `components.messages` — message-wrapper reuse via `#/components/messages/X`. Shared wrappers produce one Go payload type, not one per reference. Operation-level refs may target either `#/channels/.../messages/Y` (channel-scoped) or `#/components/messages/Y` (component-scoped).
- [x] `messages.X.payload` via `$ref` to external JSON Schema file
- [x] `messages.X.payload` inline (Go type name defaults to message key; inline `title` overrides)
- [x] Spec extension `x-aapi-codegen.schema-packages` — cross-tree `$ref` → Go-import mapping
- [x] Spec extension `x-aapi-codegen.omit-publishers` / `omit-subscribers` (**v0.2**) — opt out of either generated section even when the spec has matching operations
- [ ] `oneOf` payload dispatch (typed `Send` method per discriminated variant) — IR has the placeholder; templates pending

### AMQP binding ([spec](https://github.com/asyncapi/bindings/tree/master/amqp))

- [x] `exchange.name` — emitted as a string literal in the publisher body
- [x] `exchange.type: direct`, `topic`, `fanout`, `headers`
- [x] `bindingVersion` — preserved in IR for future use
- [x] `is: routingKey` — address is the routing key on Exchange (publisher mode)
- [x] `is: queue` (**v0.2**) — address is the queue name; subscriber emits `Subscribe<Msg>` against `bindings.amqp.queue.{name, durable, autoDelete, exclusive}`
- [x] Message-level bindings (**v0.3**): `contentEncoding`, `messageType`
- [x] Operation-level bindings (**v0.3**): `priority`, `expiration`
- [ ] AMQP delivery headers / correlation-id / reply-id surfaced to receive handlers — subscriber currently passes only `(ctx, routingKey, body)` to the dispatch wrapper

### Generated runtime contracts (**v0.2+**, **v0.3+**)

- **Publisher** ([example](internal/test/widgetservice/publisher_assertions.txt)):
  - `Transport.Publish(ctx, exchange, routingKey string, body []byte, props PublishProperties) error` — adapt your `*amqp091.Channel` by wrapping it.
  - `Send<MessageName>(ctx, ...params, msg, opts ...SendOption) error` — spec bindings become defaults; opts override per call.
  - Helpers: `WithContentType`, `WithContentEncoding`, `WithMessageType`, `WithPriority`, `WithExpirationMillis`.
- **Subscriber** ([example](internal/test/consumerservice/consumer_assertions.txt)):
  - `SubscribeTransport.Subscribe(ctx, queueName, handler func(ctx, routingKey, body) error) error` — blocks until ctx cancellation or fatal transport error.
  - `<MessageName>Handler.Handle<MessageName>(ctx, msg) error` — implement on your consumer.
  - **Ack semantics**: `nil` → ack; `errors.Is(err, ErrDrop)` → nack-no-requeue (poison); any other err → nack-with-requeue. The dispatch wrapper joins `json.Unmarshal` failures with `ErrDrop` so malformed payloads can never loop forever.

### Other protocol bindings

- [ ] Kafka
- [ ] WebSocket
- [ ] HTTP / SSE
- [ ] AMQP 1.0 (Solace, Azure Service Bus, …)

The IR's `Binding` interface is binding-agnostic; adding a new binding is "add a typed struct + a template branch", no IR refactor needed.

### JSON Schema support (inherited from go-jsonschema)

aapi-codegen delegates the JSON Schema → Go pass to [plheide/go-jsonschema](https://github.com/plheide/go-jsonschema) (a patched fork of [omissis/go-jsonschema](https://github.com/omissis/go-jsonschema)). aapi-codegen passes `--strict-additional-properties=respect-schema`, `--validate-formats=all`, `--struct-name-from-title`, `--tags json`, and `--capitalization` per the configured initialism list. See the fork's README for the supported JSON Schema surface (object/array/primitives, `$defs`, cross-file `$ref`, `oneOf`/`allOf`/`anyOf`, enum, format validation, etc.).

### Validation features

- [x] `required` fields enforced at `json.Unmarshal` time (default; opt-out via `omit-validation`)
- [x] `additionalProperties: false` enforced (rejects extra keys)
- [x] `format` validation: `uuid`, `email`, `uri`, `uri-reference`, `hostname`, `regex`
- [x] `enum`, numeric bounds, string length/pattern, array constraints (inherited from go-jsonschema)
- [x] Per-spec / per-config opt-out via `x-aapi-codegen.omit-validation: true` (or `omit-validation: true` in `aapi-codegen.config.yaml`) — for consumers that hand-write their own `UnmarshalJSON`

### CLI / configuration

- [x] Minimal invocation: `aapi-codegen SPEC.asyncapi.yaml`
- [x] `-o OUT.go` (defaults to `./types.gen.go`)
- [x] `-package PKG` (defaults: basename of output dir, with `vN` parent-prepending rule)
- [x] `-config FILE.yaml` (optional; for repos that want one canonical knob)
- [x] Additive `capitalization` over `DefaultInitialisms = [ID, URL]`
- [x] `omit-validation: true` (config or spec extension) suppresses generated `UnmarshalJSON`

### Distribution

- [x] `go build ./cmd/aapi-codegen` from a sibling-checkout layout
- [x] GoReleaser config + GitHub Actions workflow for release archives ([`.goreleaser.yaml`](.goreleaser.yaml), [`.github/workflows/release.yaml`](.github/workflows/release.yaml))
- [x] Download-and-cache consumer script ([`examples/use-aapi-codegen.sh`](examples/use-aapi-codegen.sh))
- [ ] `go install` from a fully-resolved module — blocked on dropping the `replace` directive (requires either upstream-merging the go-jsonschema patches, or remapping the fork's module path).
