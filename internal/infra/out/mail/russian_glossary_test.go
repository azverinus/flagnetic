package mail_test

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// retiredWords are the Russian renderings the product no longer uses: each
// has one chosen word, and a template that drifts back to the old one makes
// the console and the mail name the same thing differently. The console's
// catalog keeps the same list in its own test.
var retiredWords = regexp.MustCompile(`(?i)экземпляр|экспозиц|сеанс|корзин|аварийн|сопровождающ|перебазир|ретеншен|вариаци|раскатк|переопредел|предуслов|набор\w* правил|\(а\)|единиц`)

func TestTheRussianTemplatesUseNoRetiredWord(t *testing.T) {
	files, err := filepath.Glob("templates/ru/*")
	if err != nil || len(files) == 0 {
		t.Fatalf("no Russian templates found (err %v)", err)
	}
	for _, file := range files {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if word := retiredWords.Find(body); word != nil {
			t.Errorf("%s uses the retired word %q; use the term of the Russian glossary", file, word)
		}
	}
}
