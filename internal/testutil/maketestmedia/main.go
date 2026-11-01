// Command maketestmedia generates the test media tree (used by scripts/make-test-media.sh).
package main

import (
	"fmt"
	"os"

	"github.com/wtfits/homestreaming/internal/testutil"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: maketestmedia <dir>")
		os.Exit(2)
	}
	if err := testutil.MakeTree(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
