package grpc

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestReleaseSourceIsAnExecutable(t *testing.T) {
	payload, err := os.ReadFile("agent.codefly.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Source struct{ Directory, Agent string }
		Release struct{ Owner string }
	}
	if err := yaml.Unmarshal(payload, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Release.Owner != "cli" || manifest.Source.Directory != "cmd/toolbox-grpc" || manifest.Source.Agent == "" {
		t.Fatal("release must select its command entrypoint, source packager and sole publisher")
	}
	command := exec.Command("go", "list", "-mod=readonly", "-f", "{{.Name}}", ".")
	command.Dir = manifest.Source.Directory
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("inspect source entrypoint: %v: %s", err, output)
	}
	if strings.TrimSpace(string(output)) != "main" {
		t.Fatalf("source selects a library, not an executable: %s", output)
	}
}
