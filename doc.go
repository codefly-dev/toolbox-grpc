// Package grpc is the codefly gRPC toolbox — reflection-based
// introspection of any gRPC server, exposed as typed Tool RPCs.
//
// This is the canonical replacement for `bash -c "grpcurl ..."`.
// Agents that need to discover what a gRPC service exposes call
// typed RPCs here; the Bash toolbox refuses every `grpcurl`
// invocation and routes callers via canonical_for: [grpcurl].
//
// Why this is not just "shell out to grpcurl": the same way the git
// toolbox uses go-git, the grpc toolbox talks reflection directly
// over a gRPC connection. No external binary needed, no parsing of
// grpcurl's text output, structured results all the way through.
//
// Phase 1 ships introspection only — the safe surface:
//   - grpc.list_services    — every service exposed by the target
//   - grpc.describe_service — methods + their request/response types
//   - grpc.describe_method  — full type descriptor for one method
//
// Phase 2 will add `grpc.call` (unary RPC invocation with JSON-shaped
// args + structured response). Calling requires JSON↔proto dynamic
// marshaling via protoreflect/dynamicpb — non-trivial but standard
// once we accept the dependency surface. Held back from Phase 1 so
// the introspection contract lands first and stabilizes.
//
// Permissions: this toolbox declares `canonical_for: [grpcurl]`.
// Sandbox: deny most reads/writes, network ALLOWED to the target
// the agent is calling (one connection per Tool call; no persistent
// upstream).
package grpc
