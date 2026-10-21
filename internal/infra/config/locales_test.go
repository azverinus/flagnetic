package config_test

import (
	"os"
	"regexp"
	"slices"
	"strconv"
	"testing"

	"github.com/azverinus/flagnetic/internal/infra/config"
)

// The languages FLAGNETIC_DEFAULT_LOCALE accepts are the ones the console
// is translated into: the list the console resolves a locale against and
// the catalogs its build compiles. A language added to one and not the
// others would be accepted and silently not rendered, or rendered and
// refused as the default.
func TestConsoleLocalesAreTheLanguagesTheConsoleOffers(t *testing.T) {
	for _, source := range []struct {
		file, list string
		// extra are languages a list holds that no member is offered.
		extra []string
	}{
		{file: "../../../web/apps/console/src/i18n/locale.ts", list: `export const locales = \[([^\]]*)\]`},
		{file: "../../../web/apps/console/lingui.config.ts", list: `\blocales: \[([^\]]*)\]`, extra: []string{"pseudo"}},
	} {
		data, err := os.ReadFile(source.file)
		if err != nil {
			t.Fatal(err)
		}
		match := regexp.MustCompile(source.list).FindSubmatch(data)
		if match == nil {
			t.Fatalf("%s: no locale list matching %s", source.file, source.list)
		}
		var got []string
		for _, quoted := range regexp.MustCompile(`"[^"]*"`).FindAll(match[1], -1) {
			locale, err := strconv.Unquote(string(quoted))
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(source.extra, locale) {
				got = append(got, locale)
			}
		}
		if !slices.Equal(got, config.ConsoleLocales) {
			t.Errorf("%s offers %q, config.ConsoleLocales %q", source.file, got, config.ConsoleLocales)
		}
	}
}
