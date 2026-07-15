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
// The surface is:
//   - grpc.list_services    — every service exposed by the target
//   - grpc.describe_service — methods + their request/response types
//   - grpc.describe_method  — full type descriptor for one method
//   - grpc.call             — dynamic unary invocation with JSON input/output
//
// grpc.call is marked destructive because reflection cannot determine whether
// an arbitrary application RPC mutates state. Streaming RPCs are rejected.
//
// Permissions: this toolbox declares `canonical_for: [grpcurl]`.
// Sandbox: deny most reads/writes, network ALLOWED to the target
// the agent is calling (one connection per Tool call; no persistent
// upstream).
package grpc
