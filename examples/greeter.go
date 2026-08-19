// Command greeter is the sample file to run the apply loop against.
// It is deliberately small and boring: the interesting part is what the
// pipeline does to it, not what it does.
package main

import (
	"fmt"
	"strings"
)

// Greet returns a greeting for name.
func Greet(name string) string {
	if strings.TrimSpace(name) == "" {
		name = "world"
	}
	return fmt.Sprintf("Hello, %s!", name)
}

// GreetAll greets everyone in the list, in order.
func GreetAll(names []string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, Greet(n))
	}
	return out
}

func main() {
	for _, line := range GreetAll([]string{"Ada", "Grace", ""}) {
		fmt.Println(line)
	}
}
