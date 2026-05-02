// Command toolbox-grpc is the standalone binary form of the codefly
// gRPC introspection toolbox plugin. Loaded via the standard agent
// loader (core/agents/manager.Load); registers a Toolbox server
// through agents.Serve.
//
// Configuration:
//
//	CODEFLY_TOOLBOX_VERSION — Identity version. Default "0.0.0-dev".
package main

import (
	"os"

	"github.com/codefly-dev/core/agents"
	grpctoolbox "github.com/codefly-dev/toolbox-grpc"
)

func main() {
	version := envOrDefault("CODEFLY_TOOLBOX_VERSION", "0.0.0-dev")
	agents.Serve(agents.PluginRegistration{
		Toolbox: grpctoolbox.New(version),
	})
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
