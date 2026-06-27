// Command ocm-base-context writes the build context (Containerfile + manager
// scripts) for the published base image docker.io/mroger78/ocm-base into a
// directory, so CI can build and push it with `docker buildx build <dir>`.
//
// It reuses runtime.WriteBaseBuildContext, the exact same recipe the app uses
// when it has to build a base locally, so the published image never drifts from
// what the binary expects.
//
// Usage:
//
//	ocm-base-context -o <dir> [-from debian:stable-slim]
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/mickael-menu/opencode-manager/internal/runtime"
)

func main() {
	out := flag.String("o", "", "output directory for the build context (required)")
	from := flag.String("from", "debian:stable-slim", "base distro image the recipe builds FROM")
	flag.Parse()

	if *out == "" {
		fmt.Fprintln(os.Stderr, "ocm-base-context: -o <dir> is required")
		os.Exit(2)
	}

	if err := os.MkdirAll(*out, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "ocm-base-context: create output dir: %v\n", err)
		os.Exit(1)
	}

	// Prebuilt is false: render the full base recipe (this IS the prebuilt base).
	// Packages/Commands are empty: the published base carries only the defaults;
	// user extras are layered on at runtime as a thin overlay.
	containerfile, err := runtime.WriteBaseBuildContext(*out, runtime.BaseBuildSpec{
		FromImage: *from,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "ocm-base-context: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(containerfile)
}
