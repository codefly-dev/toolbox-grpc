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
	"github.com/codefly-dev/core/agents"
	coretoolbox "github.com/codefly-dev/core/toolbox"
	grpctoolbox "github.com/codefly-dev/toolbox-grpc"
)

func main() {
	agents.ServeToolbox(grpctoolbox.New(coretoolbox.Version()))
}
