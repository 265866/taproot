// Package builtin exposes the built-in translator registrations for SDK users.
package builtin

import (
	sdktranslator "github.com/265866/taproot/sdk/translator"

	_ "github.com/265866/taproot/internal/translator"
)

// Registry exposes the default registry populated with all built-in translators.
func Registry() *sdktranslator.Registry {
	return sdktranslator.Default()
}

// Pipeline returns a pipeline that already contains the built-in translators.
func Pipeline() *sdktranslator.Pipeline {
	return sdktranslator.NewPipeline(sdktranslator.Default())
}
