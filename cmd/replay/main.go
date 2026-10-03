package main

import (
	"fmt"
	ledger "github.com/Hirama/ledger-core-demo"
	"os"
)

func main() {
	book, err := ledger.Replay()
	if err != nil {
		fmt.Fprintln(os.Stderr, "replay failed:", err)
		os.Exit(1)
	}
	ledger.PrintReport(os.Stdout, book)
}
