package main

import (
	"fmt"

	"github.com/eushing/agentwork/internal/paths"
)

// pathsCmd prints the resolved machine-side layout. The daemon has the same
// command (`agentwork-daemon paths`) so operators can compare the host and the
// machine side of the link; both honor AGENTWORK_HOME.
func pathsCmd() {
	fmt.Println(paths.Current().JSON())
}
