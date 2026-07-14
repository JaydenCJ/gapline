// Command gapline reads a log stream, autodetects its timestamp format,
// and renders the time delta between consecutive lines so gaps stand out.
package main

import (
	"os"

	"github.com/JaydenCJ/gapline/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
