// Command protoc-gen-kiln-mcp is a protoc plugin that generates MCP tool
// definitions (JSON Schemas and dispatch code) from KilnService RPCs annotated
// with kiln.options.v1 MCP options.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "protoc-gen-kiln-mcp: not implemented yet")
	os.Exit(1)
}
