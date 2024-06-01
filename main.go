/*
Copyright © 2022 buchhalter.ai <support@buchhalter.ai>
*/
package main

import (
	"buchhalter/cmd"
)

var (
	// version of the software.
	// Typically a branch or tag name.
	// Is set at compile time via ldflags.
	cliVersion = "main-development"

	// commitHash reflects the current git sha.
	// Is set at compile time via ldflags.
	commitHash = "none"

	// buildTime is the compile date + time.
	// Is set at compile time via ldflags.
	buildTime = "unknown"
)

func main() {
	cmd.Execute(cliVersion, commitHash, buildTime)
}
