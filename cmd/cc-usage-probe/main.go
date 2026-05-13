// Command cc-usage-probe is a one-shot helper used during development to
// verify the /usage endpoint returns the expected shape.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/dns/cc-monitor/internal/usage"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	c := usage.NewClient()
	snap, err := c.Fetch(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fetch error: %v\n", err)
		os.Exit(1)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(snap)
}
