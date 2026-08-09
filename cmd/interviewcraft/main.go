package main

import (
	"os"

	"github.com/interviewcraft/interviewcraft/internal/cli"
)

func main() {
	os.Exit(cli.RunOS(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
