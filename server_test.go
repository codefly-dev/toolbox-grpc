package grpc_test

import (
	"context"
	"net"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
	"google.golang.org/protobuf/types/known/structpb"

	toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
	grpctoolbox "github.com/codefly-dev/toolbox-grpc"
)

// startReflectionServer spins up a real gRPC server on an ephemeral
// port with the toolbox v0 Toolbox service + reflection registered.
// Returns the address the toolbox should dial. The server is torn
// down via t.Cleanup so tests don't leak goroutines or sockets.
//
// We register a no-op UnimplementedToolboxServer (the actual RPCs
// would error if called) — these tests only exercise reflection,
// which doesn't invoke the methods.
func startReflectionServer(t *testing.T) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	srv := grpc.NewServer()
	toolboxv0.RegisterToolboxServer(srv, &toolboxv0.UnimplementedToolboxServer{})
	reflection.Register(srv)

	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() {
		srv.Stop()
	})
	return lis.Addr().String()
}

// --- Schema / dispatch tests (no live server needed) -------------

func TestGRPC_Identity(t *testing.T) {
	srv := grpctoolbox.New("0.0.1")
	resp, err := srv.Identity(context.Background(), &toolboxv0.IdentityRequest{})
	require.NoError(t, err)
	require.Equal(t, "grpc", resp.Name)
	require.Equal(t, "0.0.1", resp.Version)
	require.Equal(t, []string{"grpcurl"}, resp.CanonicalFor,
		"grpc toolbox owns the `grpcurl` binary in the canonical-routing layer")
	require.NotEmpty(t, resp.SandboxSummary)
}

func TestGRPC_ListTools_Stable(t *testing.T) {
	srv := grpctoolbox.New("0.0.1")
	resp, err := srv.ListTools(context.Background(), &toolboxv0.ListToolsRequest{})
	require.NoError(t, err)

	names := make([]string, 0, len(resp.Tools))
	for _, tl := range resp.Tools {
		names = append(names, tl.Name)
	}
	require.ElementsMatch(t, []string{
		"grpc.list_services",
		"grpc.describe_service",
		"grpc.describe_method",
		"grpc.call",
	}, names, "if the surface changes, pin it here")
}

func TestGRPC_UnknownTool_ActionableError(t *testing.T) {
	srv := grpctoolbox.New("0.0.1")
	resp, err := srv.CallTool(context.Background(), &toolboxv0.CallToolRequest{Name: "grpc.bogus"})
	require.NoError(t, err)
	require.NotEmpty(t, resp.Error)
	require.Contains(t, resp.Error, "grpc.bogus")
	require.Contains(t, resp.Error, "ListToolSummaries")
}

func TestGRPC_ListServices_RequiresAddress(t *testing.T) {
	srv := grpctoolbox.New("0.0.1")
	resp, err := srv.CallTool(context.Background(), &toolboxv0.CallToolRequest{
		Name: "grpc.list_services",
	})
	require.NoError(t, err)
	require.NotEmpty(t, resp.Error)
	// As of core@v0.1.159, registry.Base auto-validates against
	// InputSchema BEFORE dispatch. Validator phrasing is
	// "missing property 'address'" — accept either form.
	require.True(t,
		strings.Contains(resp.Error, "address") || strings.Contains(resp.Error, "required"),
		"missing address must surface a clear hint; got %q", resp.Error)
}

func TestGRPC_DescribeService_RequiresBothFields(t *testing.T) {
	srv := grpctoolbox.New("0.0.1")
	args, _ := structpb.NewStruct(map[string]any{"address": "localhost:50051"})
	resp, err := srv.CallTool(context.Background(), &toolboxv0.CallToolRequest{
		Name:      "grpc.describe_service",
		Arguments: args,
	})
	require.NoError(t, err)
	require.NotEmpty(t, resp.Error)
	require.Contains(t, resp.Error, "service")
}

func TestGRPC_Call_NotImplemented_DispatchesCleanly(t *testing.T) {
	// Phase 1 stub. Confirms the dispatch path is wired so swapping
	// the body is a one-line change later.
	srv := grpctoolbox.New("0.0.1")
	resp, err := srv.CallTool(context.Background(), &toolboxv0.CallToolRequest{
		Name: "grpc.call",
	})
	require.NoError(t, err)
	require.NotEmpty(t, resp.Error)
	require.Contains(t, resp.Error, "not yet implemented")
}

// --- Live-server reflection tests --------------------------------

func TestGRPC_ListServices_AgainstRealServer(t *testing.T) {
	addr := startReflectionServer(t)

	srv := grpctoolbox.New("0.0.1")
	args, _ := structpb.NewStruct(map[string]any{"address": addr})
	resp, err := srv.CallTool(context.Background(), &toolboxv0.CallToolRequest{
		Name:      "grpc.list_services",
		Arguments: args,
	})
	require.NoError(t, err)
	require.Empty(t, resp.Error, "list against real server must succeed: %s", resp.Error)

	out := resp.Content[0].GetStructured().AsMap()
	services, _ := out["services"].([]any)
	names := make([]string, 0, len(services))
	for _, s := range services {
		if v, ok := s.(string); ok {
			names = append(names, v)
		}
	}
	sort.Strings(names)
	// We registered codefly.services.toolbox.v0.Toolbox; reflection
	// also surfaces grpc.reflection.v1.ServerReflection (and its
	// v1alpha sibling, depending on grpc-go version). Pin the one we
	// know we registered.
	require.Contains(t, names, "codefly.services.toolbox.v0.Toolbox",
		"the service we explicitly registered must appear in reflection list")
}

func TestGRPC_DescribeService_ReturnsToolboxMethods(t *testing.T) {
	addr := startReflectionServer(t)

	srv := grpctoolbox.New("0.0.1")
	args, _ := structpb.NewStruct(map[string]any{
		"address": addr,
		"service": "codefly.services.toolbox.v0.Toolbox",
	})
	resp, err := srv.CallTool(context.Background(), &toolboxv0.CallToolRequest{
		Name:      "grpc.describe_service",
		Arguments: args,
	})
	require.NoError(t, err)
	require.Empty(t, resp.Error, "describe must succeed: %s", resp.Error)

	out := resp.Content[0].GetStructured().AsMap()
	require.Equal(t, "codefly.services.toolbox.v0.Toolbox", out["service"])
	methods, _ := out["methods"].([]any)
	require.NotEmpty(t, methods, "Toolbox service has 7 RPCs; expected non-empty list")

	// Walk the method list and confirm Identity / ListTools / CallTool
	// are present — those are the load-bearing RPCs of the contract.
	names := make(map[string]bool, len(methods))
	for _, m := range methods {
		entry, _ := m.(map[string]any)
		if n, ok := entry["name"].(string); ok {
			names[n] = true
		}
	}
	for _, expected := range []string{"Identity", "ListTools", "CallTool"} {
		require.True(t, names[expected], "method %q must appear in describe_service output; got %v", expected, names)
	}
}

func TestGRPC_DescribeMethod_ReturnsTypeNames(t *testing.T) {
	addr := startReflectionServer(t)

	srv := grpctoolbox.New("0.0.1")
	args, _ := structpb.NewStruct(map[string]any{
		"address": addr,
		"service": "codefly.services.toolbox.v0.Toolbox",
		"method":  "Identity",
	})
	resp, err := srv.CallTool(context.Background(), &toolboxv0.CallToolRequest{
		Name:      "grpc.describe_method",
		Arguments: args,
	})
	require.NoError(t, err)
	require.Empty(t, resp.Error, "describe_method must succeed: %s", resp.Error)

	out := resp.Content[0].GetStructured().AsMap()
	require.Equal(t, "Identity", out["name"])
	require.Equal(t, "codefly.services.toolbox.v0.IdentityRequest", out["input_type"])
	require.Equal(t, "codefly.services.toolbox.v0.IdentityResponse", out["output_type"])
	require.Equal(t, false, out["client_streaming"])
	require.Equal(t, false, out["server_streaming"])
}

func TestGRPC_DescribeMethod_UnknownMethod_ProducesActionableError(t *testing.T) {
	addr := startReflectionServer(t)

	srv := grpctoolbox.New("0.0.1")
	args, _ := structpb.NewStruct(map[string]any{
		"address": addr,
		"service": "codefly.services.toolbox.v0.Toolbox",
		"method":  "BogusMethod",
	})
	resp, err := srv.CallTool(context.Background(), &toolboxv0.CallToolRequest{
		Name:      "grpc.describe_method",
		Arguments: args,
	})
	require.NoError(t, err)
	require.NotEmpty(t, resp.Error)
	require.Contains(t, resp.Error, "BogusMethod",
		"error must name the bad method so the agent knows what was rejected")
}

func TestGRPC_DescribeService_UnknownService_ProducesActionableError(t *testing.T) {
	addr := startReflectionServer(t)

	srv := grpctoolbox.New("0.0.1")
	args, _ := structpb.NewStruct(map[string]any{
		"address": addr,
		"service": "no.such.Service",
	})
	resp, err := srv.CallTool(context.Background(), &toolboxv0.CallToolRequest{
		Name:      "grpc.describe_service",
		Arguments: args,
	})
	require.NoError(t, err)
	require.NotEmpty(t, resp.Error,
		"unknown service must surface as a tool error")
}
