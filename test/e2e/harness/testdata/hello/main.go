// Command hello prints the version stamped in at build time. It exists so the
// harness tests can prove that BuildPackage caches its output and injects the
// version the same way BuildBinary does.
package main

import "fmt"

var version = "dev"

func main() {
	fmt.Println(version)
}
