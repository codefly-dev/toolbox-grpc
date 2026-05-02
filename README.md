# toolbox-grpc

A codefly toolbox plugin for gRPC reflection-based introspection.
Canonical owner of the `grpcurl` binary — the
`codefly-dev/toolbox-bash` plugin refuses every `grpcurl`
invocation and routes callers here.

## Tools (read-only)

- `grpc.list_services(address, timeout_ms?)` — opens a short-lived
  reflection stream, returns service names alphabetically.
- `grpc.describe_service(address, service, timeout_ms?)` — returns
  the service's methods with input_type / output_type and the
  client/server streaming flags.
- `grpc.describe_method(address, service, method, timeout_ms?)` —
  same shape, narrowed to one method.
- `grpc.call(...)` — Phase 2 stub, returns "not yet implemented."
  The dispatch is in place so a later iteration only swaps the body.

## Connection lifecycle

Each call dials the target, performs the reflection roundtrip, and
tears the connection down. Connections are NOT pooled — short-lived
is the safer-by-default position; an attacker (or buggy agent)
can't carry state across calls.

The target server must have gRPC reflection enabled. Most codefly
agents do (via `agents.Serve` registering grpc/reflection); arbitrary
upstream servers may need an explicit reflection registration.

## Configuration

| Env var                     | Default       | Purpose                                        |
| --------------------------- | ------------- | ---------------------------------------------- |
| `CODEFLY_TOOLBOX_VERSION`   | `0.0.0-dev`   | Identity version surfaced via `Identity()`     |

The toolbox uses `insecure` transport credentials — the policy
decision about TLS lives at the host level. A future iteration can
take a per-call TLS config arg.

## Build & test

```bash
go build ./...
go test ./...
```

## Contract

This plugin implements the codefly Toolbox gRPC contract defined in
[`codefly-dev/core`](https://github.com/codefly-dev/core) at
`proto/codefly/services/toolbox/v0/toolbox.proto`.
