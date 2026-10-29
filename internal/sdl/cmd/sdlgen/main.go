// Command sdlgen writes the canonical schema file. It runs from `go generate ./...`, after
// gqlgen, so the file always follows the generated server — see package sdl.
package main

import (
	"flag"
	"log"
	"os"

	"github.com/obcode/tallox.go/internal/sdl"
)

func main() {
	out := flag.String("o", sdl.FileName, "where to write the schema")
	flag.Parse()

	if err := os.WriteFile(*out, sdl.Render(sdl.Served()), 0o644); err != nil { //nolint:gosec // a committed source file, readable by design
		log.Fatal(err)
	}
}
