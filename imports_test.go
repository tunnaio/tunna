package tunna_test

import (
	"go/build"
	"strings"
	"testing"
)

// ADR-0005: the root package is the core. It imports nothing from this module
// and nothing from the standard library that performs I/O. Types-only packages
// such as io, context, time and errors are fine. Logging is not: the core
// returns errors, adapters decide what to log.
var forbiddenPrefixes = []string{
	"os",
	"net",
	"database/sql",
	"syscall",
	"log",
	"io/fs",
	"github.com/tunnaio/tunna",
}

func TestRootPackageImportsOnlyPureStdlib(t *testing.T) {
	pkg, err := build.ImportDir(".", 0)
	if err != nil {
		t.Fatalf("reading package in current directory: %v", err)
	}
	for _, imp := range pkg.Imports {
		for _, bad := range forbiddenPrefixes {
			if imp == bad || strings.HasPrefix(imp, bad+"/") {
				t.Errorf("root package imports %q, which ADR-0005 forbids (matches %q)", imp, bad)
			}
		}
		resolved, err := build.Import(imp, "", build.FindOnly)
		if err != nil {
			t.Errorf("resolving import %q: %v", imp, err)
			continue
		}
		if !resolved.Goroot {
			t.Errorf("root package imports %q, which is not in the standard library", imp)
		}
	}
}
