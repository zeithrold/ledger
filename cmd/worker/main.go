// Command worker runs the Ledger background River worker.
package main

import (
	"os"

	"github.com/zeithrold/ledger/internal/jobs"
	"github.com/zeithrold/ledger/internal/observability"
)

func main() { os.Exit(observability.Run("ledger-worker", jobs.Run)) }
