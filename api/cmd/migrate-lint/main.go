// Command migrate-lint rejects destructive or non-transactional statements in
// the forward migrations that the API applies automatically at startup.
package main

import (
	"fmt"
	"os"

	"spm-platform/api/internal/migrate"
)

func main() {
	issues, err := migrate.LintEmbedded()
	if err != nil {
		fmt.Fprintf(os.Stderr, "migrate-lint: %v\n", err)
		os.Exit(1)
	}
	if len(issues) > 0 {
		for _, issue := range issues {
			fmt.Fprintln(os.Stderr, issue.String())
		}
		fmt.Fprintf(os.Stderr, "migrate-lint: %d unsafe statement(s) found\n", len(issues))
		os.Exit(1)
	}
	fmt.Println("migrate-lint: forward migrations are expand-only and transaction-safe")
}
