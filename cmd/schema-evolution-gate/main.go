// Command schema-evolution-gate fails pull requests that make breaking
// changes to Kafka/event schemas (Avro and JSON Schema), using Confluent
// Schema Registry compatibility semantics without a running registry.
package main

import "os"

// Exit codes.
const (
	exitOK       = 0
	exitBreaking = 1
	exitUsage    = 2
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}
