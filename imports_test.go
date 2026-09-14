package tunna_test

import (
	"go/build"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// ADR-0005 import rules, enforced per package directory. "module" imports
// are those under the module path; everything else must be standard library.
//
//   - root (tunna): no module imports; no stdlib I/O (os, net, database/sql,
//     syscall, log, io/fs). io, context, time, errors are types and allowed;
//     net/url is allowed by name, being parsing rather than I/O.
//   - sig: no module imports; standard library only; no net/http.
//   - internal/<adapter>: may import the root and sig; never another adapter.
//     Third-party imports are allowed only where listed in adapterDeps: the
//     SQLite driver in internal/sqlite (ADR-0002), nothing else yet.
//   - cmd/tunna: anything.

const modulePath = "github.com/tunnaio/tunna"

// adapterDeps lists the third-party module paths each adapter may import.
// Adding an entry is a dependency decision and belongs in the ledger.
var adapterDeps = map[string][]string{
	"internal/sqlite": {"modernc.org/sqlite"},
}

type rule struct {
	allowModule    []string // module import paths allowed, exact
	forbidPrefixes []string // stdlib prefixes forbidden
	allowExact     []string // stdlib paths allowed even under a forbidden prefix
}

var rules = map[string]rule{
	// net/url is string parsing with no I/O; the CORS origin matcher (ADR-0009)
	// needs it and it is the one thing under net/ the root may see.
	".":   {forbidPrefixes: []string{"os", "net", "database/sql", "syscall", "log", "io/fs"}, allowExact: []string{"net/url"}},
	"sig": {forbidPrefixes: []string{"os", "net/http", "database/sql", "syscall", "log"}},
}

func TestImportGraph(t *testing.T) {
	dirs := packageDirs(t)
	for _, dir := range dirs {
		dir := dir
		t.Run(filepath.ToSlash(dir), func(t *testing.T) {
			pkg, err := build.ImportDir(dir, 0)
			if err != nil {
				if _, ok := err.(*build.NoGoError); ok {
					return
				}
				t.Fatalf("reading package: %v", err)
			}
			checkImports(t, dir, pkg.Imports)
		})
	}
}

func checkImports(t *testing.T, dir string, imports []string) {
	t.Helper()
	rel := filepath.ToSlash(dir)
	if rel == "." {
		rel = "."
	}
	r, hasRule := rules[rel]
	isAdapter := strings.HasPrefix(rel, "internal/")
	isCmd := strings.HasPrefix(rel, "cmd/")

	for _, imp := range imports {
		inModule := imp == modulePath || strings.HasPrefix(imp, modulePath+"/")

		switch {
		case isCmd:
			// wiring may import anything
		case isAdapter:
			if inModule && imp != modulePath && imp != modulePath+"/sig" {
				t.Errorf("%s imports %q: adapters may import only the root and sig", rel, imp)
			}
		case hasRule:
			if inModule {
				t.Errorf("%s imports %q: must not import from the module", rel, imp)
			}
			for _, bad := range r.forbidPrefixes {
				if slices.Contains(r.allowExact, imp) {
					break
				}
				if imp == bad || strings.HasPrefix(imp, bad+"/") {
					t.Errorf("%s imports %q, forbidden by ADR-0005 (matches %q)", rel, imp, bad)
				}
			}
		default:
			t.Errorf("%s has no import rule; add one to imports_test.go", rel)
		}

		if !inModule && (hasRule || isAdapter) {
			if allowedDep(rel, imp) {
				continue
			}
			resolved, err := build.Import(imp, "", build.FindOnly)
			if err != nil || !resolved.Goroot {
				t.Errorf("%s imports %q, which is not in the standard library or in adapterDeps", rel, imp)
			}
		}
	}
}

// allowedDep reports whether imp is, or is inside, a third-party module the
// adapter at rel is permitted to import.
func allowedDep(rel, imp string) bool {
	for _, dep := range adapterDeps[rel] {
		if imp == dep || strings.HasPrefix(imp, dep+"/") {
			return true
		}
	}
	return false
}

// packageDirs lists every directory under the module root that holds Go
// files, as paths relative to the root, skipping hidden and vendor dirs.
func packageDirs(t *testing.T) []string {
	t.Helper()
	var dirs []string
	seen := map[string]bool{}
	err := filepath.WalkDir(".", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() && path != "." && (strings.HasPrefix(name, ".") || name == "vendor" || name == "testdata" || name == "sdk" || name == "spec" || name == "docs") {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(name, ".go") {
			dir := filepath.Dir(path)
			if !seen[dir] {
				seen[dir] = true
				dirs = append(dirs, dir)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking module: %v", err)
	}
	return dirs
}
