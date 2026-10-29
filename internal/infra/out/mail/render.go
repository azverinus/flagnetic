// Package mail is the outbound mail adapter: the templates every kind of
// mail is written from, in every language, and the SMTP sender.
package mail

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"strings"
	texttemplate "text/template"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	"github.com/azverinus/flagnetic/internal/domain/access"
)

//go:embed templates
var templates embed.FS

// EmbeddedTemplates returns the templates the binary holds.
func EmbeddedTemplates() fs.FS {
	sub, err := fs.Sub(templates, "templates")
	if err != nil {
		panic(err) // the directory is embedded above
	}
	return sub
}

// Rendered is a mail written for its recipient: a subject and the same
// message as text and as HTML.
type Rendered struct {
	Subject string
	Text    string
	HTML    string
}

type set struct {
	subject, text *texttemplate.Template
	html          *template.Template
}

// Renderer writes mail from the templates of each kind and language, the
// text parts with text/template and the HTML part with html/template, which
// escapes what a template is given.
type Renderer struct {
	sets map[string]set
}

// NewRenderer returns the renderer of the embedded templates.
func NewRenderer() (*Renderer, error) { return NewRendererFS(EmbeddedTemplates()) }

// NewRendererFS returns the renderer of the templates in fsys, laid out as
// <language>/<kind>.subject.txt, <kind>.txt and <kind>.html. It refuses
// templates in which a kind lacks a language or a part, or that do not
// parse, so a mail that cannot be written is found when the replica starts.
func NewRendererFS(fsys fs.FS) (*Renderer, error) {
	r := &Renderer{sets: map[string]set{}}
	for _, locale := range access.MailLocales {
		for _, kind := range access.MailKinds {
			base := locale + "/" + string(kind)
			subject, err := texttemplate.ParseFS(fsys, base+".subject.txt")
			if err != nil {
				return nil, fmt.Errorf("the %s mail in %s: %w", kind, locale, err)
			}
			text, err := texttemplate.ParseFS(fsys, base+".txt")
			if err != nil {
				return nil, fmt.Errorf("the %s mail in %s: %w", kind, locale, err)
			}
			html, err := template.ParseFS(fsys, base+".html")
			if err != nil {
				return nil, fmt.Errorf("the %s mail in %s: %w", kind, locale, err)
			}
			r.sets[key(kind, locale)] = set{subject: subject, text: text, html: html}
		}
	}
	return r, nil
}

func key(kind access.MailKind, locale string) string { return string(kind) + "/" + locale }

// view is what a template reads.
type view struct {
	Recipient appaccess.MailRecipient
	Data      any
}

// Render writes m in its language. The subject is one line whatever the
// template holds.
func (r *Renderer) Render(m appaccess.Mail) (Rendered, error) {
	s, ok := r.sets[key(m.Kind, m.Locale)]
	if !ok {
		return Rendered{}, fmt.Errorf("no template for a %s mail in %q", m.Kind, m.Locale)
	}
	v := view{Recipient: m.To, Data: m.Data}
	var subject, text, html bytes.Buffer
	if err := s.subject.Execute(&subject, v); err != nil {
		return Rendered{}, fmt.Errorf("writing the %s subject: %w", m.Kind, err)
	}
	if err := s.text.Execute(&text, v); err != nil {
		return Rendered{}, fmt.Errorf("writing the %s text: %w", m.Kind, err)
	}
	if err := s.html.Execute(&html, v); err != nil {
		return Rendered{}, fmt.Errorf("writing the %s HTML: %w", m.Kind, err)
	}
	return Rendered{Subject: strings.Join(strings.Fields(subject.String()), " "), Text: text.String(), HTML: html.String()}, nil
}
