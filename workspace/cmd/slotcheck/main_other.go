//go:build !linux

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "slotcheck runs on a Linux slot host only")
	os.Exit(2)
}
