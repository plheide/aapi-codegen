# aapi-codegen

[![GitHub release](https://img.shields.io/github/v/release/plheide/aapi-codegen?include_prereleases&style=flat)](https://github.com/plheide/aapi-codegen/releases)
[![GitHub Workflow Status (event)](https://img.shields.io/github/actions/workflow/status/plheide/aapi-codegen/development.yaml?style=flat)](https://github.com/plheide/aapi-codegen/actions?workflow=development)
[![Go Reference](https://pkg.go.dev/badge/github.com/plheide/aapi-codegen.svg)](https://pkg.go.dev/github.com/plheide/aapi-codegen)
[![License](https://img.shields.io/github/license/plheide/aapi-codegen?style=flat)](LICENSE)
[![GitHub go.mod Go version](https://img.shields.io/github/go-mod/go-version/plheide/aapi-codegen?style=flat)](go.mod)

AsyncAPI 3.x → Go code generator, the AsyncAPI counterpart to [oapi-codegen](https://github.com/oapi-codegen/oapi-codegen). From one AsyncAPI spec it generates one Go file with typed payloads and AMQP publishers and subscribers. The JSON Schema → Go pass is [go-jsonschema](https://github.com/plheide/go-jsonschema), used as a library.

## What it generates

- **Payload types** for every message, with `UnmarshalJSON` methods that enforce the schema ([Validation](#validation)).
- **A Publisher** with one method per `action: send` operation, named after the operation key: `sendOrder` becomes `SendOrder(ctx, ...params, msg, opts ...SendOption) error`. The spec's AMQP bindings set the default message properties; `SendOption`s override them per call.
- **A Subscriber** with a `<Msg>Handler` interface and a `Subscribe<Msg>(ctx, ...params, handler) error` method per `action: receive` operation. Several receive operations on one queue share a single `Subscribe<Queue>` method that dispatches by routing key.
- **Typed channel parameters.** A string `enum` becomes `type X string` with one constant per value (`JobTypeBuild`). A string `pattern` becomes `type X string` with `NewX` / `MustX` constructors that check the pattern.

The generated package doesn't import an AMQP client. You implement two small interfaces, `Transport` and `SubscribeTransport`, around the client you use, such as `amqp091-go`.

## Install

```shell
go install github.com/plheide/aapi-codegen/cmd/aapi-codegen@latest
```

Or run it from a `go:generate` directive without installing it:

```go
//go:generate go run github.com/plheide/aapi-codegen/cmd/aapi-codegen@v0.9.0 -o types.gen.go spec.asyncapi.yaml
```

Pin the version in `go:generate`, so that regenerating doesn't silently pick up a newer release.

Release archives for Linux, macOS and Windows (amd64 and arm64) are on the [releases page](https://github.com/plheide/aapi-codegen/releases). To pin a version per repository without a Go toolchain, copy [examples/use-aapi-codegen.sh](examples/use-aapi-codegen.sh). It downloads the archive once, caches the binary and prints its path; it works on Linux, macOS and Windows (Git Bash or WSL).

## Usage

```shell
aapi-codegen SPEC.asyncapi.yaml
aapi-codegen -package PKG -o OUT.go SPEC.asyncapi.yaml
aapi-codegen -config aapi-codegen.config.yaml SPEC.asyncapi.yaml
```

| Flag | Default |
| --- | --- |
| `-o` | `./types.gen.go` |
| `-package` | The output directory's name. For a version directory (`v1`, `v2`, …) the parent's name comes first: `lib/schemas/order-event/v1` → `ordereventv1`. |
| `-config` | None. A YAML file with any of `package`, `output`, `schema-packages`, `message-packages`, `omit-validation` and `capitalization`; see [Spec extensions](#spec-extensions). |

## Spec extensions

aapi-codegen reads its settings from an `x-aapi-codegen` extension in the spec, so they live next to the contract.

| Where | Key | Effect | Since |
| --- | --- | --- | --- |
| root `x-aapi-codegen` | `schema-packages` | Maps a schema `$id` to a Go import. A payload `$ref` into that schema uses the imported type instead of generating a copy. | v0.1 |
| root `x-aapi-codegen` | `message-packages` | Maps a producer's spec file to its generated Go package. See [Consuming another service's messages](#consuming-another-services-messages). | v0.5 |
| root `x-aapi-codegen` | `omit-validation` | No validating `UnmarshalJSON`; pattern-typed channel parameters fall back to `string`. | v0.1 |
| root `x-aapi-codegen` | `omit-publishers`, `omit-subscribers` | Skips that part of the output, even when the spec has matching operations. | v0.2 |
| channel `bindings.x-aapi-codegen` | `queue: {name, durable, autoDelete, exclusive}` | The queue a consumer reads from when the channel is in `is: routingKey` mode, where AMQP binding 0.3.0 forbids `bindings.amqp.queue`. If both are set, `bindings.amqp.queue` wins. | v0.5.1 |

```yaml
x-aapi-codegen:
  schema-packages:
    - id: https://schemas.example.com/common/v1/header.schema.json
      package: example.com/lib/schemas/common/v1
      alias: commonv1
```

The optional `-config` file accepts only `package`, `output`, `schema-packages`, `message-packages`, `omit-validation`, and `capitalization` (initialisms added to the default `ID` and `URL`); any other key is an error. `omit-publishers`, `omit-subscribers` and the `queue` binding work only in the spec. Either the spec or the config file can opt out of validation; neither overrides the other's opt-out.

## Inline payloads and `components`

Payloads can be declared inline in the spec, with no separate schema file:

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

The payload's Go type is named after the message key (`InlineMessage`); an inline `title` overrides it. Types in `components.schemas` are generated once and shared. A payload can also `$ref` an external JSON Schema file.

Shared message definitions work the same way through `components.messages`: define the message once and `$ref` it as `#/components/messages/X` from any number of channels, and it generates one Go type. Operations may reference a message either through its channel (`#/channels/<channel>/messages/<key>`) or through `#/components/messages/<key>`.

## Consuming another service's messages

A consumer can use the message types its producer already generated, instead of generating its own copy. In the consumer's spec, `$ref` the producer's message across files and map the producer's spec file to its Go package:

```yaml
# consumer spec
x-aapi-codegen:
  message-packages:
    - file: ../producer/producer.asyncapi.yaml
      package: example.com/producer/ordersv1
      alias: ordersv1
  omit-publishers: true

channels:
  orderConsumer:
    address: '{tenant}.orders'
    parameters:
      tenant: { description: Tenant identifier. }
    bindings:
      amqp:
        is: queue
    messages:
      OrderMessage:
        $ref: '../producer/producer.asyncapi.yaml#/channels/orderDispatch/messages/OrderMessage'

operations:
  consumeOrder:
    action: receive
    channel: { $ref: '#/channels/orderConsumer' }
    messages:
      - $ref: '#/channels/orderConsumer/messages/OrderMessage'
```

The generated handler takes the producer's type:

```go
type OrderMessageHandler interface {
    HandleOrderMessage(ctx context.Context, msg ordersv1.OrderMessage) error
}
```

- `file` is matched against the file part of the `$ref`, with both resolved relative to the consumer's spec.
- The last segment of the `$ref` is used as the type name in the producer's package. That's the producer's message key, so the producer's payload must not set a different `title`.
- A consumer-only spec usually sets `omit-publishers: true`.

The [`consumerimportservice`](internal/test/consumerimportservice/) fixture is a complete producer/consumer pair.

## Publisher

```go
type Transport interface {
    Publish(ctx context.Context, exchange, routingKey string, body []byte, props PublishProperties) error
}

pub := NewPublisher(transport)
err := pub.SendOrder(ctx, "acme", msg, WithPriority(5))
```

- There is one method per `action: send` operation, named after the operation key with its first letter capitalised (`sendOrder` → `SendOrder`).
- Its arguments are `ctx`, one argument per address parameter in address order, the message, then options.
- The routing key is the channel address with its parameters filled in. The exchange is `bindings.amqp.exchange.name`.
- The default properties come from the spec:
  - the content type from the message's `contentType`, or else `defaultContentType`;
  - `contentEncoding` and `messageType` from the message's AMQP binding;
  - `priority` and `expiration` from the operation's AMQP binding.
- Override them per call with `WithContentType(string)`, `WithContentEncoding(string)`, `WithMessageType(string)`, `WithPriority(uint8)` and `WithExpirationMillis(int)`. Later options win.

## Subscriber

```go
type SubscribeTransport interface {
    Subscribe(ctx context.Context, queueName string, bindingKeys []string,
        handler func(ctx context.Context, routingKey string, body []byte) error) error
}

type OrderMessageHandler interface {
    HandleOrderMessage(ctx context.Context, msg OrderMessage) error
}

sub := NewSubscriber(transport)
err := sub.SubscribeOrderMessage(ctx, "acme", handler) // blocks until ctx is done
```

- There is one `<Msg>Handler` interface and one `Subscribe<Msg>` method per `action: receive` operation, named after the message type.
- The queue name comes from `bindings.amqp.queue.name`, then `bindings.x-aapi-codegen.queue.name`, then the channel address when the channel has `is: queue`. A receive operation needs one of these.
- The binding key is always the channel address, so the queue's name can differ from its routing key.
- Your transport maps the handler's result to AMQP:
  - `nil` → ack;
  - an error that matches `errors.Is(err, ErrDrop)` → nack without requeue (a poison message);
  - any other error → nack with requeue.

  A body that fails to decode is joined with `ErrDrop`, so a malformed message never loops.
- **Shared queues.** When several receive operations resolve to the same queue, you get one `Subscribe<Queue>` method. Its `<Queue>Handler` interface embeds each message type's handler once. It binds every operation's routing key and dispatches each delivery by routing key; keys that carry the same message type go to the same handler method. The operations must share an exchange and use fixed addresses with distinct routing keys.
- Queue properties (`durable`, `autoDelete`, `exclusive`) are read but not used. Generated code doesn't declare queues or exchanges; your transport or your infrastructure owns the topology.

## Validation

By default, the generated `UnmarshalJSON` methods enforce the schema:

- `required` fields and `additionalProperties: false`;
- `enum`, numeric and string bounds, `pattern`, and array constraints;
- the formats `uuid`, `email`, `uri`, `uri-reference`, `hostname` and `regex`.

Invalid JSON fails at `json.Unmarshal` with an error that names the violation.

Some formats change the Go type instead:

- `date-time` → `time.Time`;
- `date` and `time` → `types.SerializableDate` and `types.SerializableTime` from `github.com/plheide/go-jsonschema/pkg/types`. That package then becomes a dependency of your module.

To write your own `UnmarshalJSON`, opt out. You might do this to accept a legacy wire format alongside the canonical one; two `UnmarshalJSON` methods on one type would not compile.

```yaml
x-aapi-codegen:
  omit-validation: true
```

The same key works in the `-config` file.

## Examples

Worked specs and assertions live under [`internal/test/`](internal/test/):

| Fixture | Exercises |
| --- | --- |
| [`widgetservice/`](internal/test/widgetservice/) | direct exchange, templated and literal addresses, cross-tree `schema-packages` import |
| [`notificationservice/`](internal/test/notificationservice/) | topic exchange, single-parameter address |
| [`inlineservice/`](internal/test/inlineservice/) | inline payloads and `components.schemas` |
| [`sharedmsgservice/`](internal/test/sharedmsgservice/) | `components.messages` referenced from several channels |
| [`messagecollisionservice/`](internal/test/messagecollisionservice/) | inline and component message keys that collide |
| [`validationservice/`](internal/test/validationservice/) | the `omit-validation` opt-out |
| [`consumerservice/`](internal/test/consumerservice/) | `action: receive`, `<Msg>Handler`, `Subscribe<Msg>`, `ErrDrop`, queue-mode channels |
| [`bindingsservice/`](internal/test/bindingsservice/) | `SendOption`, message and operation AMQP bindings |
| [`enumparams/`](internal/test/enumparams/) | enum-typed channel parameters, shared by publisher and subscriber |
| [`validatedparams/`](internal/test/validatedparams/) | pattern-typed parameters with `NewX` / `MustX`; `omit-validation` falls back to `string` |
| [`consumerimportservice/`](internal/test/consumerimportservice/) | a consumer importing its producer's message type through `message-packages` |
| [`sharedqueueservice/`](internal/test/sharedqueueservice/) | a routing-key consumer whose queue name differs from the channel address |
| [`multimessagequeueservice/`](internal/test/multimessagequeueservice/) | several receive operations on one queue, dispatched by routing key |
| [`sharedmsgqueueservice/`](internal/test/sharedmsgqueueservice/) | one message type on two routing keys of a shared queue, and on a queue of its own |

## Feature coverage

### AsyncAPI 3.x ([spec](https://www.asyncapi.com/docs/reference/specification/v3.1.0))

- [x] `asyncapi: 3.x` version detection
- [x] `info.title` (used in the generated doc comments)
- [x] `channels`
  - [x] `address`: literal (`widget.cancellation`) or templated (`{tenant}.{widgetType}`)
  - [x] `parameters`: `string` by default, typed from `schema.type: string` with `enum` or `pattern`
  - [x] `messages`, including several per channel
  - [x] `bindings.amqp`
- [x] `operations`
  - [x] `action: send` → a `Publisher` method named after the operation key
  - [x] `action: receive` → `<Msg>Handler` and `Subscriber.Subscribe<Msg>`, or a shared-queue `Subscribe<Queue>`
  - [x] `channel.$ref` and `messages[].$ref` (exactly one message per operation)
- [x] `components.schemas` and `components.messages`
- [x] `messages.X.payload` inline or as a `$ref` to an external JSON Schema file
- [x] Cross-file message `$ref` with `message-packages`
- [ ] `oneOf` payloads: go-jsonschema generates the types, but there is no per-variant `Send` method

### AMQP binding ([spec](https://github.com/asyncapi/bindings/tree/master/amqp))

- [x] `exchange.name`: the exchange the publisher sends to
- [x] `exchange.type` (`direct`, `topic`, `fanout`, `headers`): used in doc comments only
- [x] `is: routingKey` and `is: queue`
- [x] `queue.{name, durable, autoDelete, exclusive}`: the name is used; the rest is read but not used
- [x] Message bindings: `contentEncoding`, `messageType`
- [x] Operation bindings: `priority`, `expiration`
- [ ] Delivery metadata (headers, correlation ID, reply-to) passed to handlers

### Other protocol bindings

- [ ] Kafka
- [ ] WebSocket
- [ ] HTTP / SSE
- [ ] AMQP 1.0

## Limitations

- **AMQP 0.9.1 only.** Every channel needs `bindings.amqp`.
- **One message per operation.**
- **Same message on two single-operation queues.** If two queues each have one receive operation and both carry the same message type, both would need the same `Subscribe<Msg>` method, so the generator stops with an error. Bind the routing keys to one shared queue instead. A shared queue gets its own `Subscribe<Queue>` method, so it can sit alongside another queue for the same type.
- **Name collisions.** If two spec elements would generate the same Go name, such as a payload titled `Subscriber`, the generator stops with an error instead of writing code that doesn't compile.
- **Shared-queue subscribers** need one exchange and fixed, distinct routing keys.
- **Payload only.** Handlers receive only the decoded payload: no headers, correlation ID or reply-to.
- **No topology.** Generated code doesn't declare queues or exchanges.
- **Unknown keys are ignored.** An unknown `x-aapi-codegen` key produces no warning, so a misspelt key does nothing.
- **Imported type names.** An imported message's Go type name is the producer's message key; see [Consuming another service's messages](#consuming-another-services-messages).

## Development

Requires Go 1.26+. The `go.mod` directive sets the floor; CI tracks the version pinned in [`.github/versions.env`](.github/versions.env).

```shell
go test ./...
```

The build is a single Go binary. The JSON Schema pass comes from the [plheide/go-jsonschema](https://github.com/plheide/go-jsonschema) fork, pinned in [go.mod](go.mod) like any other dependency; a plain clone builds.

PRs run `go mod tidy -diff`, `go vet`, `go test -race`, `golangci-lint`, and a goreleaser snapshot build via [`.github/workflows/development.yaml`](.github/workflows/development.yaml).

To release, push a `vX.Y.Z` or `vX.Y.Z-rc.N` tag; the release workflow builds the archives with goreleaser. In the change before the tag, bump the default `AAPI_VERSION` in [examples/use-aapi-codegen.sh](examples/use-aapi-codegen.sh) and the version in the `go:generate` example above.

## JSON Schema support

The JSON Schema → Go pass is [plheide/go-jsonschema](https://github.com/plheide/go-jsonschema), a fork of [omissis/go-jsonschema](https://github.com/omissis/go-jsonschema), called as a library. aapi-codegen configures it to:

- name structs after their `title`;
- respect `additionalProperties` as the schema declares it;
- validate formats;
- emit `json` tags;
- apply the configured initialisms.

See the fork's README for the supported JSON Schema features, such as `$defs`, cross-file `$ref`, `oneOf` / `allOf` / `anyOf` and `enum`.

## License

MIT; see [LICENSE](LICENSE).
