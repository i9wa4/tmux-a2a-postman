// cutover-dry-run reports a read-only inventory for an explicitly mapped
// disposable legacy fixture. It never activates a cutover.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/i9wa4/tmux-a2a-postman/internal/cutover"
)

func main() {
	legacy := flag.String("legacy-root", "", "absolute legacy fixture root")
	target := flag.String("target-root", "", "absolute target root")
	mapping := flag.String("mapping", "", "JSON file containing an array of {legacy,target} paths")
	flag.Parse()
	if *legacy == "" || *target == "" || *mapping == "" || flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "required: --legacy-root, --target-root, --mapping")
		os.Exit(2)
	}
	bytes, err := os.ReadFile(*mapping)
	if err != nil {
		fail(err)
	}
	var mappings []cutover.Mapping
	if err := json.Unmarshal(bytes, &mappings); err != nil {
		fail(err)
	}
	inventory, err := cutover.BuildInventory(*legacy, *target, mappings)
	if err != nil {
		fail(err)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(inventory); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
