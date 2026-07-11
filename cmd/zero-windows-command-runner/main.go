package main

import (
	"os"

	"github.com/BlusceLabs/green/internal/sandbox"
)

func main() {
	os.Exit(sandbox.RunWindowsSandboxCommandRunner(os.Args[1:], os.Stderr))
}
