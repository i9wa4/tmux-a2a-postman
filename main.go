package main

import (
	"fmt"
	"os"
)

var revision string

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Printf("postman dev (rev: %s)\n", revision)
		return
	}
	fmt.Println("postman: not yet implemented")
}
