package console_test

import (
	"os"
	"regexp"
	"slices"
	"strconv"
	"testing"

	"github.com/azverinus/flagnetic/internal/domain/access"
)

// The console shows a permission it has no display name for by its catalog
// name, so a role described to a member would read "flag.edit" in the
// middle of a sentence. Every permission the catalog defines has a display
// name in the console, and the console names no permission the catalog no
// longer defines.
func TestTheConsoleNamesEveryPermissionOfTheCatalog(t *testing.T) {
	const file = "../../../../../web/apps/console/src/lib/access-names.ts"
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	table := regexp.MustCompile(`(?s)const permissionNames: Record<Permission, MessageDescriptor> = \{(.*?)\n\};`).FindSubmatch(data)
	if table == nil {
		t.Fatalf("%s: no permissionNames table", file)
	}
	var named []string
	for _, key := range regexp.MustCompile(`(?m)^\s*("[^"]+"):`).FindAllSubmatch(table[1], -1) {
		name, err := strconv.Unquote(string(key[1]))
		if err != nil {
			t.Fatal(err)
		}
		named = append(named, name)
	}
	var catalog []string
	for _, p := range access.Catalog() {
		catalog = append(catalog, string(p.Name))
	}
	for _, p := range catalog {
		if !slices.Contains(named, p) {
			t.Errorf("%s has no display name for the permission %s", file, p)
		}
	}
	for _, p := range named {
		if !slices.Contains(catalog, p) {
			t.Errorf("%s names the permission %s, which the catalog does not define", file, p)
		}
	}
}
