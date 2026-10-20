// Command flagnetic is the Flagnetic server.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/azverinus/flagnetic/internal/pkg/buildinfo"
)

const usage = `Usage: flagnetic <command>

Commands:
  version   Print the version and exit
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && args[0] == "version" {
		fmt.Fprintln(stdout, buildinfo.Version())
		return 0
	}
	fmt.Fprint(stderr, usage)
	return 2
}
