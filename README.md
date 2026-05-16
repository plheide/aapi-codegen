# aapi-codegen

AsyncAPI 3.x → Go code generator. The AsyncAPI counterpart to `oapi-codegen` (same idea, different spec). Builds on `go-jsonschema` (imported as a library) for the JSON Schema → Go pass.

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

## Status

aapi-codegen can be used today for publisher-side AMQP code generation from AsyncAPI 3.x specs. Subscriber-side (`action: receive`) and additional bindings are the main open scope.

### AsyncAPI 3.x ([spec](https://www.asyncapi.com/docs/reference/specification/v3.1.0))

- [x] `asyncapi: 3.x` version detection
- [x] `info.title` (used in generated publisher doc comment)
- [x] `channels`
  - [x] `address` — literal (e.g. `widget.cancellation`)
  - [x] `address` — templated, multi-parameter (e.g. `{tenant}.{widgetType}`)
  - [x] `address` — templated, single-parameter (e.g. `{workflowName}`)
  - [x] `parameters` — typed as `string` in publisher signatures (typed parameter schemas not yet inspected)
  - [x] `messages` — multi-message channels
  - [x] `bindings.amqp` — typed per channel
- [x] `operations`
  - [x] `action: send` — emits typed `Publisher.Send<MessageName>(ctx, ...params, msg)` methods
  - [ ] `action: receive` — consumer / handler emission
  - [x] `operations.X.channel.$ref` — internal ref resolution
  - [x] `operations.X.messages[].$ref` — internal ref resolution (exactly one message per operation in v1)
- [x] `components.schemas` — shared types declared once, referenced from multiple payloads via `#/components/schemas/X`
- [x] `components.messages` — message-wrapper reuse via `#/components/messages/X`. Shared wrappers produce one Go payload type, not one per reference. Operation-level refs may target either `#/channels/.../messages/Y` (channel-scoped) or `#/components/messages/Y` (component-scoped).
- [x] `messages.X.payload` via `$ref` to external JSON Schema file
- [x] `messages.X.payload` inline (Go type name defaults to message key; inline `title` overrides)
- [x] Spec extension `x-aapi-codegen.schema-packages` — cross-tree `$ref` → Go-import mapping

### AMQP binding ([spec](https://github.com/asyncapi/bindings/tree/master/amqp))

- [x] `exchange.name` — emitted as a string literal in the publisher body
- [x] `exchange.type: direct`
- [x] `exchange.type: topic`
- [x] `exchange.type: fanout` (untested but should work — exchange type doesn't affect publisher emission, only consumer behaviour)
- [x] `exchange.type: headers` (same)
- [x] `bindingVersion` — preserved in IR for future use (not used in emission)
- [x] Routing key derived from the channel's `address` (the binding's `is:` field is currently ignored; only routing-key-shaped addresses are supported in v1)
- [ ] `is: queue` (queue-based addressing) not yet — would need IR + template branch
- [ ] AMQP message-level bindings (properties, headers, ack mode) not yet

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
