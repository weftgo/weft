package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"

	"github.com/weftgo/weft/studio"
)

// pricingFlag adds --pricing to a flag set: a pricing table file
// (studio.LoadPricing; pricing.json's shape), mirroring WEFT_PRICING one
// to one (pricingOptions reads both; the flag wins).
func pricingFlag(fs *flag.FlagSet) *string {
	return fs.String("pricing", "",
		"`path` to a pricing table (pricing.json's shape, USD per 1M tokens) that replaces the shipped one whole (default: $WEFT_PRICING, else the table shipped with this weft)")
}

// pricingOptions turns --pricing / WEFT_PRICING into studio options and
// the line the banner prints ("" for the shipped table). A file that
// does not load is a start error naming the path and the field — never
// a Studio silently pricing with the default.
func pricingOptions(flagPath string) ([]studio.Option, string, error) {
	path, from := flagPath, "--pricing"
	if path == "" {
		path, from = os.Getenv("WEFT_PRICING"), "WEFT_PRICING"
	}
	if path == "" {
		return nil, "", nil
	}
	t, err := studio.LoadPricing(path)
	if err != nil {
		return nil, "", fmt.Errorf("%s: %w", from, err)
	}
	return []studio.Option{studio.Pricing(t)}, "studio: pricing " + path + " (" + from + "), dated " + t.Dated +
		", " + strconv.Itoa(len(t.Models)) + " models", nil
}
