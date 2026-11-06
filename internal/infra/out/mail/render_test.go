package mail_test

import (
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/infra/out/mail"
)

// samples is the data each kind's template reads, written by whoever adds a
// kind: a kind without one fails the test below.
var samples = map[access.MailKind]any{
	access.MailTest: appaccess.TestMailData{Instance: "https://flags.example.com"},
	access.MailPasswordReset: appaccess.PasswordResetMailData{
		URL: "https://flags.example.com/password-reset/c2VjcmV0", ExpiresAt: time.Date(2026, 11, 6, 15, 4, 0, 0, time.UTC),
	},
}

func TestEveryKindHasEveryLanguageAndPart(t *testing.T) {
	r, err := mail.NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range access.MailKinds {
		data, ok := samples[kind]
		if !ok {
			t.Errorf("%s: no sample data in this test", kind)
			continue
		}
		for _, locale := range access.MailLocales {
			got, err := r.Render(appaccess.Mail{
				Kind: kind, Locale: locale, To: appaccess.MailRecipient{Name: "Ada", Email: "ada@example.com"}, Data: data,
			})
			if err != nil {
				t.Errorf("%s in %s: %v", kind, locale, err)
				continue
			}
			if got.Subject == "" || got.Text == "" || got.HTML == "" {
				t.Errorf("%s in %s: subject %q, text %d bytes, html %d bytes; want all three", kind, locale,
					got.Subject, len(got.Text), len(got.HTML))
			}
			if strings.ContainsAny(got.Subject, "\r\n") {
				t.Errorf("%s in %s: the subject spans lines: %q", kind, locale, got.Subject)
			}
			for _, part := range []string{got.Text, got.HTML} {
				if !strings.Contains(part, "ada@example.com") || strings.Contains(part, "<no value>") {
					t.Errorf("%s in %s: a part does not name its recipient or has an unfilled field:\n%s", kind, locale, part)
				}
			}
		}
	}
}

func TestAMissingTemplateFailsTheRenderer(t *testing.T) {
	full := mail.EmbeddedTemplates()
	for _, locale := range access.MailLocales {
		for _, kind := range access.MailKinds {
			for _, part := range []string{"subject.txt", "txt", "html"} {
				name := locale + "/" + string(kind) + "." + part
				t.Run("without "+name, func(t *testing.T) {
					without := fstest.MapFS{}
					err := fs.WalkDir(full, ".", func(p string, d fs.DirEntry, err error) error {
						if err != nil || d.IsDir() || p == name {
							return err
						}
						data, err := fs.ReadFile(full, p)
						without[p] = &fstest.MapFile{Data: data}
						return err
					})
					if err != nil {
						t.Fatal(err)
					}
					if _, err := mail.NewRendererFS(without); err == nil {
						t.Errorf("a renderer without %s was built", name)
					}
				})
			}
		}
	}
}

func TestTheRussianMailIsRussianAndNamesWhatTheTemplateIsGiven(t *testing.T) {
	r, err := mail.NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.Render(appaccess.Mail{
		Kind: access.MailTest, Locale: "ru", To: appaccess.MailRecipient{Name: "Ада <b>", Email: "ada@example.com"},
		Data: appaccess.TestMailData{Instance: "https://flags.example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Subject, "Тестовое") || !strings.Contains(got.Text, "https://flags.example.com") {
		t.Errorf("subject %q, text %q; want the Russian test mail naming the instance", got.Subject, got.Text)
	}
	if strings.Contains(got.HTML, "<b>") || !strings.Contains(got.HTML, "&lt;b&gt;") {
		t.Errorf("the HTML part does not escape the recipient's name: %s", got.HTML)
	}
}

func TestThePasswordResetMailCarriesItsLinkAndWhenItExpires(t *testing.T) {
	r, err := mail.NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	const link = "https://flags.example.com/password-reset/c2VjcmV0-_"
	for _, locale := range access.MailLocales {
		got, err := r.Render(appaccess.Mail{
			Kind: access.MailPasswordReset, Locale: locale, To: appaccess.MailRecipient{Name: "Ada", Email: "ada@example.com"},
			Data: appaccess.PasswordResetMailData{URL: link, ExpiresAt: time.Date(2026, 11, 6, 15, 4, 0, 0, time.FixedZone("x", 3*3600))},
		})
		if err != nil {
			t.Fatal(err)
		}
		// The expiry is written in UTC, whatever zone the clock reads.
		for _, part := range []string{got.Text, got.HTML} {
			if !strings.Contains(part, link) || !strings.Contains(part, "2026-11-06 12:04 UTC") {
				t.Errorf("%s: a part lacks the link or its expiry in UTC:\n%s", locale, part)
			}
		}
		if !strings.Contains(got.HTML, `href="`+link+`"`) {
			t.Errorf("%s: the HTML does not link to the reset: %s", locale, got.HTML)
		}
	}
	ru, _ := r.Render(appaccess.Mail{Kind: access.MailPasswordReset, Locale: "ru", Data: appaccess.PasswordResetMailData{URL: link}})
	if !strings.Contains(ru.Subject, "Сброс пароля") {
		t.Errorf("Russian subject = %q", ru.Subject)
	}
}
