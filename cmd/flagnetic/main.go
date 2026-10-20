// Command flagnetic is the Flagnetic server.
package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/azverinus/flagnetic/internal/infra/in/cli"
)

func main() {
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	os.Exit(cli.Main(os.Args[1:], os.LookupEnv, os.Stdout, os.Stderr, signals))
}
