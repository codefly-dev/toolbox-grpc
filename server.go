package grpc

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	reflectpb "google.golang.org/grpc/reflection/grpc_reflection_v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/structpb"

	toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
	"github.com/codefly-dev/core/toolbox/registry"
	"github.com/codefly-dev/core/toolbox/respond"
)

// DefaultDialTimeout caps any single dial+reflect call. gRPC dials
// otherwise wait forever for a connection. Configurable per-call via
// the timeout_ms argument.
const DefaultDialTimeout = 10 * time.Second

// Server implements codefly.services.toolbox.v0.Toolbox for gRPC
// reflection-based introspection.
//
// Construction is cheap; the toolbox holds no persistent connection.
// Each tool call dials the target, performs the reflection
// roundtrip, and tears the connection down. This is the safe-by-
// default position — connections are short-lived and there's no
// state for an attacker (or a buggy agent) to leak across calls.
type Server struct {
	*registry.Base

	version string
}

// New returns a Server.
func New(version string) *Server {
	s := &Server{version: version}
	s.Base = registry.NewBase(s)
	return s
}

// --- Identity ----------------------------------------------------

func (s *Server) Identity(_ context.Context, _ *toolboxv0.IdentityRequest) (*toolboxv0.IdentityResponse, error) {
	return &toolboxv0.IdentityResponse{
		Name:           "grpc",
		Version:        s.version,
		Description:    "gRPC reflection-based service/method introspection. Canonical owner of the `grpcurl` binary.",
		CanonicalFor:   []string{"grpcurl"},
		SandboxSummary: "reads: deny; writes: deny; network: allowed to the dial target (one short-lived connection per call)",
	}, nil
}

// --- Tools -------------------------------------------------------

// Tools is the source of truth — see git/server.go for convention.
func (s *Server) Tools() []*registry.ToolDefinition {
	addrSchema := map[string]any{
		"type":        "string",
		"description": "host:port of the gRPC server (no scheme).",
	}
	timeoutSchema := map[string]any{
		"type":        "integer",
		"description": "Per-call dial timeout in ms. Default 10000.",
		"minimum":     100,
		"maximum":     60000,
	}

	return []*registry.ToolDefinition{
		{
			Name:               "grpc.list_services",
			SummaryDescription: "Connect to a gRPC server and list every service it exposes via reflection. Read-only.",
			LongDescription: "Opens a short-lived gRPC connection to the target, sends a ServerReflection " +
				"ListServices request, returns the service names alphabetically. The target must have " +
				"reflection enabled (most codefly agents do via agents.Serve registering grpc/reflection).",
			InputSchema: respond.Schema(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"address":    addrSchema,
					"timeout_ms": timeoutSchema,
				},
				"required": []any{"address"},
			}),
			Tags:        []string{"grpc", "read-only", "network"},
			Idempotency: "idempotent",
			ErrorModes:  "Returns 'dial X: ...' when the server is unreachable, 'open reflection stream: ...' when reflection isn't registered, or 'reflection: ...' when the server reports an error.",
			Examples: []*toolboxv0.ToolExample{
				{
					Description:     "Discover what services a local gRPC plugin exposes.",
					Arguments:       mustGrpcStruct(map[string]any{"address": "127.0.0.1:54321"}),
					ExpectedOutcome: "{ services: ['codefly.services.toolbox.v0.Toolbox', 'grpc.health.v1.Health', ...] }",
				},
			},
			Handler: s.listServices,
		},
		{
			Name:               "grpc.describe_service",
			SummaryDescription: "List a service's methods + their request/response types via reflection. Read-only.",
			LongDescription: "Asks the server's reflection endpoint for the FileDescriptorProto containing " +
				"the named service, parses out the method list. Each method returns name, input_type, " +
				"output_type (fully-qualified), and the streaming flags (client/server streaming).",
			InputSchema: respond.Schema(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"address": addrSchema,
					"service": map[string]any{
						"type":        "string",
						"description": "Fully-qualified service name, e.g. `helloworld.Greeter`.",
					},
					"timeout_ms": timeoutSchema,
				},
				"required": []any{"address", "service"},
			}),
			Tags:        []string{"grpc", "read-only", "network"},
			Idempotency: "idempotent",
			ErrorModes:  "Returns 'service X not found' when the name doesn't exist, 'reflection: ...' on protocol errors.",
			Examples: []*toolboxv0.ToolExample{
				{
					Description:     "Inspect the Toolbox service's methods.",
					Arguments:       mustGrpcStruct(map[string]any{"address": "127.0.0.1:54321", "service": "codefly.services.toolbox.v0.Toolbox"}),
					ExpectedOutcome: "{ service, methods: [{ name: 'Identity', input_type, output_type, ... }, ...] }",
				},
			},
			Handler: s.describeService,
		},
		{
			Name:               "grpc.describe_method",
			SummaryDescription: "Describe one method on a service (input/output type names). Read-only. Composes before grpc.call.",
			LongDescription: "Same reflection roundtrip as grpc.describe_service but narrows to a single " +
				"method. Useful when the LLM already knows the service and just needs the method's input " +
				"shape before composing arguments.",
			InputSchema: respond.Schema(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"address": addrSchema,
					"service": map[string]any{
						"type":        "string",
						"description": "Fully-qualified service name.",
					},
					"method": map[string]any{
						"type":        "string",
						"description": "Method name within the service.",
					},
					"timeout_ms": timeoutSchema,
				},
				"required": []any{"address", "service", "method"},
			}),
			Tags:        []string{"grpc", "read-only", "network"},
			Idempotency: "idempotent",
			ErrorModes:  "Returns 'method X not found on service Y' when the name doesn't exist, 'reflection: ...' on protocol errors.",
			Examples: []*toolboxv0.ToolExample{
				{
					Description:     "Look up the Identity RPC's signature.",
					Arguments:       mustGrpcStruct(map[string]any{"address": "127.0.0.1:54321", "service": "codefly.services.toolbox.v0.Toolbox", "method": "Identity"}),
					ExpectedOutcome: "{ name: 'Identity', input_type: '...IdentityRequest', output_type: '...IdentityResponse', client_streaming: false, server_streaming: false }",
				},
			},
			Handler: s.describeMethod,
		},
		{
			Name:               "grpc.call",
			SummaryDescription: "Invoke a unary RPC with JSON args. Phase 2 stub — currently returns 'not implemented'.",
			LongDescription: "Will invoke a unary RPC with JSON-shaped arguments converted to the method's " +
				"input message via dynamicpb. Phase 1 of the grpc toolbox doesn't implement this — " +
				"introspection (list_services / describe_service / describe_method) is sufficient for " +
				"discovery flows. Calling grpc.call today returns an actionable error so the agent can " +
				"fall back; the dispatch case is in place so a later commit only swaps the body.",
			InputSchema: respond.Schema(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"address": addrSchema,
					"service": map[string]any{"type": "string"},
					"method":  map[string]any{"type": "string"},
					"request": map[string]any{
						"type":        "object",
						"description": "Request body as a JSON object matching the method's input message.",
					},
					"timeout_ms": timeoutSchema,
				},
				"required": []any{"address", "service", "method"},
			}),
			Tags:        []string{"grpc", "network", "stub"},
			Idempotency: "unknown",
			ErrorModes:  "Always returns 'grpc.call not yet implemented; introspection (list_services / describe_service / describe_method) is usable today' until the body lands.",
			Examples: []*toolboxv0.ToolExample{
				{
					Description:     "Phase 2 will invoke arbitrary RPCs.",
					Arguments:       mustGrpcStruct(map[string]any{"address": "127.0.0.1:54321", "service": "codefly.services.toolbox.v0.Toolbox", "method": "Identity"}),
					ExpectedOutcome: "Currently returns the not-implemented error.",
				},
			},
			Handler: s.callStub,
		},
	}
}

func mustGrpcStruct(m map[string]any) *structpb.Struct {
	s, err := structpb.NewStruct(m)
	if err != nil {
		panic(fmt.Sprintf("grpc toolbox: cannot encode example args: %v", err))
	}
	return s
}

// callStub is the Phase 1 stub for grpc.call. Wired through Handler
// so a later iteration just swaps the body without touching the
// dispatch surface.
func (s *Server) callStub(_ context.Context, _ *toolboxv0.CallToolRequest) *toolboxv0.CallToolResponse {
	return respond.Error("grpc.call not yet implemented; introspection (list_services / describe_service / describe_method) is usable today")
}

// --- Tool implementations ----------------------------------------

func (s *Server) listServices(ctx context.Context, req *toolboxv0.CallToolRequest) *toolboxv0.CallToolResponse {
	args := respond.Args(req)
	address, ok := args["address"].(string)
	if !ok || address == "" {
		return respond.Error("grpc.list_services: address is required")
	}
	timeout := timeoutFromArgs(args)

	services, err := withReflectStream(ctx, address, timeout, func(stream reflectpb.ServerReflection_ServerReflectionInfoClient) ([]string, error) {
		return reflectListServices(stream)
	})
	if err != nil {
		return respond.Error("grpc.list_services: %v", err)
	}

	// Sort for determinism — agents diff'ing successive calls
	// shouldn't see spurious churn just because the server walked the
	// service map in a different order.
	sort.Strings(services)
	out := make([]any, len(services))
	for i, sv := range services {
		out[i] = sv
	}
	return respond.Struct(map[string]any{"services": out})
}

func (s *Server) describeService(ctx context.Context, req *toolboxv0.CallToolRequest) *toolboxv0.CallToolResponse {
	args := respond.Args(req)
	address, _ := args["address"].(string)
	service, _ := args["service"].(string)
	if address == "" || service == "" {
		return respond.Error("grpc.describe_service: address and service are required")
	}
	timeout := timeoutFromArgs(args)

	methods, err := withReflectStream(ctx, address, timeout, func(stream reflectpb.ServerReflection_ServerReflectionInfoClient) ([]methodInfo, error) {
		return reflectDescribeService(stream, service)
	})
	if err != nil {
		return respond.Error("grpc.describe_service: %v", err)
	}

	out := make([]any, len(methods))
	for i, m := range methods {
		out[i] = map[string]any{
			"name":             m.Name,
			"input_type":       m.InputType,
			"output_type":      m.OutputType,
			"client_streaming": m.ClientStreaming,
			"server_streaming": m.ServerStreaming,
		}
	}
	return respond.Struct(map[string]any{
		"service": service,
		"methods": out,
	})
}

func (s *Server) describeMethod(ctx context.Context, req *toolboxv0.CallToolRequest) *toolboxv0.CallToolResponse {
	args := respond.Args(req)
	address, _ := args["address"].(string)
	service, _ := args["service"].(string)
	method, _ := args["method"].(string)
	if address == "" || service == "" || method == "" {
		return respond.Error("grpc.describe_method: address, service, and method are required")
	}
	timeout := timeoutFromArgs(args)

	info, err := withReflectStream(ctx, address, timeout, func(stream reflectpb.ServerReflection_ServerReflectionInfoClient) (*methodInfo, error) {
		methods, err := reflectDescribeService(stream, service)
		if err != nil {
			return nil, err
		}
		for _, m := range methods {
			if m.Name == method {
				m := m
				return &m, nil
			}
		}
		return nil, fmt.Errorf("method %q not found on service %q", method, service)
	})
	if err != nil {
		return respond.Error("grpc.describe_method: %v", err)
	}

	return respond.Struct(map[string]any{
		"service":          service,
		"name":             info.Name,
		"input_type":       info.InputType,
		"output_type":      info.OutputType,
		"client_streaming": info.ClientStreaming,
		"server_streaming": info.ServerStreaming,
	})
}

// --- Reflection plumbing -----------------------------------------

// methodInfo is the toolbox's own (lightweight) view of a method —
// just the fields callers care about, decoupled from the proto
// descriptor types so the response shape is JSON-stable.
type methodInfo struct {
	Name            string
	InputType       string
	OutputType      string
	ClientStreaming bool
	ServerStreaming bool
}

// withReflectStream dials the target, opens a reflection stream,
// runs fn against it, and tears everything down. Generic over the
// caller's return type so each tool gets typed results without
// rewriting the dial-and-stream boilerplate.
func withReflectStream[T any](
	ctx context.Context, address string, timeout time.Duration,
	fn func(reflectpb.ServerReflection_ServerReflectionInfoClient) (T, error),
) (T, error) {
	var zero T
	dialCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// NewClient + insecure creds: we only do read-side reflection;
	// the policy decision about TLS vs insecure is the host's, not
	// the toolbox's. A future iteration can take a TLS config arg.
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return zero, fmt.Errorf("dial %s: %w", address, err)
	}
	defer conn.Close()

	client := reflectpb.NewServerReflectionClient(conn)
	stream, err := client.ServerReflectionInfo(dialCtx)
	if err != nil {
		return zero, fmt.Errorf("open reflection stream: %w", err)
	}
	defer func() { _ = stream.CloseSend() }()

	return fn(stream)
}

// reflectListServices issues a ListServices request and reads the
// single reply. The reflection stream is bidirectional but each
// query is a request/response pair; we don't pipeline.
func reflectListServices(stream reflectpb.ServerReflection_ServerReflectionInfoClient) ([]string, error) {
	if err := stream.Send(&reflectpb.ServerReflectionRequest{
		MessageRequest: &reflectpb.ServerReflectionRequest_ListServices{ListServices: ""},
	}); err != nil {
		return nil, fmt.Errorf("send ListServices: %w", err)
	}
	resp, err := stream.Recv()
	if err != nil {
		if err == io.EOF {
			return nil, fmt.Errorf("server closed reflection stream without reply")
		}
		return nil, fmt.Errorf("recv ListServices: %w", err)
	}
	if errResp := resp.GetErrorResponse(); errResp != nil {
		return nil, fmt.Errorf("reflection: %s (code %d)", errResp.GetErrorMessage(), errResp.GetErrorCode())
	}
	listResp := resp.GetListServicesResponse()
	if listResp == nil {
		return nil, fmt.Errorf("reflection: ListServicesResponse missing")
	}
	out := make([]string, 0, len(listResp.GetService()))
	for _, sv := range listResp.GetService() {
		out = append(out, sv.GetName())
	}
	return out, nil
}

// reflectDescribeService asks the server for the FileDescriptor
// containing the named service, parses it, and returns the methods.
// FileContainingSymbol is the standard "give me the file that
// defines X" reflection query — same one grpcurl uses internally.
func reflectDescribeService(stream reflectpb.ServerReflection_ServerReflectionInfoClient, service string) ([]methodInfo, error) {
	if err := stream.Send(&reflectpb.ServerReflectionRequest{
		MessageRequest: &reflectpb.ServerReflectionRequest_FileContainingSymbol{
			FileContainingSymbol: service,
		},
	}); err != nil {
		return nil, fmt.Errorf("send FileContainingSymbol: %w", err)
	}
	resp, err := stream.Recv()
	if err != nil {
		return nil, fmt.Errorf("recv FileContainingSymbol: %w", err)
	}
	if errResp := resp.GetErrorResponse(); errResp != nil {
		return nil, fmt.Errorf("reflection: %s (code %d)", errResp.GetErrorMessage(), errResp.GetErrorCode())
	}
	fd := resp.GetFileDescriptorResponse()
	if fd == nil {
		return nil, fmt.Errorf("reflection: FileDescriptorResponse missing")
	}

	// The reflection server may return the requested file plus its
	// transitive dependencies. Find the one that actually defines
	// the requested service.
	for _, raw := range fd.GetFileDescriptorProto() {
		var fdp descriptorpb.FileDescriptorProto
		if err := proto.Unmarshal(raw, &fdp); err != nil {
			return nil, fmt.Errorf("unmarshal FileDescriptorProto: %w", err)
		}
		// service is fully-qualified ("pkg.Service"); fdp.Package is
		// "pkg" and Service.Name is "Service".
		shortName := service
		if fdp.GetPackage() != "" {
			prefix := fdp.GetPackage() + "."
			if strings.HasPrefix(service, prefix) {
				shortName = strings.TrimPrefix(service, prefix)
			} else {
				continue
			}
		}
		for _, sd := range fdp.GetService() {
			if sd.GetName() != shortName {
				continue
			}
			methods := make([]methodInfo, 0, len(sd.GetMethod()))
			for _, md := range sd.GetMethod() {
				methods = append(methods, methodInfo{
					Name:            md.GetName(),
					InputType:       strings.TrimPrefix(md.GetInputType(), "."),
					OutputType:      strings.TrimPrefix(md.GetOutputType(), "."),
					ClientStreaming: md.GetClientStreaming(),
					ServerStreaming: md.GetServerStreaming(),
				})
			}
			return methods, nil
		}
	}
	return nil, fmt.Errorf("service %q not found in any returned FileDescriptorProto", service)
}

// timeoutFromArgs reads the timeout_ms argument with the toolbox's
// default floor. Callers always get a positive duration.
func timeoutFromArgs(args map[string]any) time.Duration {
	if v, ok := args["timeout_ms"].(float64); ok && v > 0 {
		return time.Duration(v) * time.Millisecond
	}
	return DefaultDialTimeout
}
