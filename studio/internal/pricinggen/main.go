// Command pricinggen writes Studio's default pricing table,
// studio/pricing.json, from studio/internal/pricingdata (plan A5). It
// runs from studio/pricing.go's go:generate line; the output depends on
// the table alone, so a second run changes nothing.
//
//	go run ./internal/pricinggen -o pricing.json
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/weftgo/weft/studio/internal/pricingdata"
)

func main() {
	out := flag.String("o", "pricing.json", "the file to write")
	flag.Parse()
	b, err := pricingdata.JSON()
	if err != nil {
		fmt.Fprintln(os.Stderr, "pricinggen:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*out, b, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "pricinggen:", err)
		os.Exit(1)
	}
}
