// Command dra-inspect provides read-only inspection and diagnostics for
// Kubernetes Dynamic Resource Allocation environments.
package main

import (
	"os"

	"github.com/johnahull/k8s-dra-inspect/internal/cli"
)

var version = "dev"

func main() {
	os.Exit(cli.Execute(version))
}
