// Package builtins embeds the built-in YAML program definitions that ship
// with the server binary. The package is deliberately small — it only
// provides the file system so that internal/programs/registry.go can read
// each program at startup without any ../.. embed path gymnastics.
//
// Adding a new built-in program:
//  1. Drop a .yaml file into this directory following the same format as
//     nomad-cluster.yaml (name, runtime, meta, config, resources, outputs).
//  2. Call RegisterYAML(r, id, displayName, description, builtins.ReadFile("my-program.yaml"))
//     in internal/programs/registry.go's RegisterBuiltins function.
package builtins

import (
	"embed"
	"fmt"
)

//go:embed *.yaml
var fs embed.FS

// ReadFile returns the raw contents of a built-in program YAML file by name
// (e.g. "nomad-cluster.yaml"). Panics on missing files — those represent a
// compile-time programming error (a file was referenced but not added to the
// directory).
func ReadFile(name string) string {
	b, err := fs.ReadFile(name)
	if err != nil {
		panic(fmt.Sprintf("builtins: missing embedded file %q: %v", name, err))
	}
	return string(b)
}
