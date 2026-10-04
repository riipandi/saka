package mailer

import (
	"bytes"
	"errors"
	"fmt"
	htmltemplate "html/template"
	"io"
	"io/fs"
	"path"
	"slices"
	"strings"
	texttemplate "text/template"
)

// Body is the two renderings of one message. Both are produced for every
// template, so a recipient whose client refuses HTML still reads the message.
type Body struct {
	HTML string
	Text string
}

// View is what a template renders against besides the sender identity: the
// recipient's address where a template shows it, and the template's own fields
// under Data.
//
// Data is a value the template reflects into — a struct with the field names the
// template uses (`{{.Data.ResetLink}}`), or a map with the same keys. A template
// naming a field the value does not have fails the render rather than printing
// an empty string, so a renamed field is caught by the test that renders it.
type View struct {
	// Email is the recipient a template shows, for the templates that name it.
	Email string
	// Data carries the template's own fields.
	Data any
}

// Sender is the identity every template renders with. It is resolved by the
// caller once, so a template never reads the configuration itself.
type Sender struct {
	AppName string
	LogoURL string
}

// Templates renders the React Email templates a caller supplies as an fs.FS.
//
// The HTML and the text rendering of a template are parsed as one unit, so a
// template that cannot be parsed fails construction rather than a send. The
// compiled files are produced by the Vite build (plugins/plugin-email.ts), so a
// parse failure here is a broken build, not a bad configuration.
type Templates struct {
	sender Sender
	// html and text hold one parsed template per name. The two sets are kept
	// apart because html/template escapes what it writes and text/template does
	// not, and a plain-text body must not arrive HTML-escaped.
	html map[string]*htmltemplate.Template
	text map[string]*texttemplate.Template
}

// NewTemplatesFS parses the templates under dir of fsys. The directory layout is
// the one the Vite build writes: one `<name>_html.tmpl` and one `<name>_text.tmpl`
// per template, each defining the block "root".
//
// It exists so a test renders fixtures without rebuilding the embedded set.
func NewTemplatesFS(fsys fs.FS, sender Sender) (*Templates, error) {
	entries, err := fs.Glob(fsys, "email/*.tmpl")
	if err != nil {
		return nil, fmt.Errorf("mailer: list templates: %w", err)
	}
	if len(entries) == 0 {
		return nil, errors.New("mailer: no email templates are embedded; run the Vite build")
	}

	t := &Templates{
		sender: sender,
		html:   make(map[string]*htmltemplate.Template, len(entries)),
		text:   make(map[string]*texttemplate.Template, len(entries)),
	}
	for _, entry := range entries {
		source, readErr := fs.ReadFile(fsys, entry)
		if readErr != nil {
			return nil, fmt.Errorf("mailer: read %s: %w", entry, readErr)
		}
		name, kind := templateName(path.Base(entry))
		if name == "" {
			return nil, fmt.Errorf("mailer: %s is not a <name>_<html|text>.tmpl file", entry)
		}
		if err := t.parse(name, kind, source); err != nil {
			return nil, fmt.Errorf("mailer: %s: %w", entry, err)
		}
	}

	// A template with one rendering and not the other cannot produce a body, so
	// the pair is required rather than filled in with an empty half.
	for name := range t.html {
		if _, ok := t.text[name]; !ok {
			return nil, fmt.Errorf("mailer: template %q has no text rendering", name)
		}
	}
	for name := range t.text {
		if _, ok := t.html[name]; !ok {
			return nil, fmt.Errorf("mailer: template %q has no html rendering", name)
		}
	}
	return t, nil
}

// parse adds one rendering to the set.
func (t *Templates) parse(name, kind string, source []byte) error {
	if kind == kindText {
		parsed, err := texttemplate.New(name).Parse(string(source))
		if err != nil {
			return err
		}
		t.text[name] = parsed
		return nil
	}
	parsed, err := htmltemplate.New(name).Parse(string(source))
	if err != nil {
		return err
	}
	t.html[name] = parsed
	return nil
}

// The two renderings a template name resolves to.
const (
	kindHTML = "html"
	kindText = "text"
)

// templateName splits a compiled file name into the template it belongs to and
// which rendering it is.
func templateName(file string) (name, kind string) {
	switch {
	case strings.HasSuffix(file, "_"+kindHTML+".tmpl"):
		return strings.TrimSuffix(file, "_"+kindHTML+".tmpl"), kindHTML
	case strings.HasSuffix(file, "_"+kindText+".tmpl"):
		return strings.TrimSuffix(file, "_"+kindText+".tmpl"), kindText
	default:
		return "", ""
	}
}

// Names lists the templates this binary carries, sorted. It is what a command
// reports and what a test iterates.
func (t *Templates) Names() []string {
	names := make([]string, 0, len(t.html))
	for name := range t.html {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// Has reports whether a template name is one this binary carries.
func (t *Templates) Has(name string) bool {
	_, ok := t.html[name]
	return ok
}

// Render produces both renderings of one template as strings. It is the
// convenient form for a caller that wants the body in hand — a test, a preview,
// a body sent some other way.
//
// The send path does not use it: RenderTo writes one rendering straight to a
// writer, so a message never becomes a string on its way to the session.
func (t *Templates) Render(name string, view View) (Body, error) {
	var body Body
	var htmlOut, textOut bytes.Buffer
	if err := t.RenderTo(&htmlOut, name, BodyHTML, view); err != nil {
		return Body{}, err
	}
	if err := t.RenderTo(&textOut, name, BodyText, view); err != nil {
		return Body{}, err
	}
	body.HTML = htmlOut.String()
	body.Text = textOut.String()
	return body, nil
}

// RenderTo renders one rendering of one template into w.
//
// The sender identity comes from the Templates value, so a caller supplies only
// what is specific to this message. An unknown name is refused rather than
// rendered as an empty body.
func (t *Templates) RenderTo(w io.Writer, name string, kind BodyKind, view View) error {
	parsed, err := t.parsed(name, kind)
	if err != nil {
		return err
	}
	context := struct {
		AppName string
		LogoURL string
		Email   string
		Data    any
	}{
		AppName: t.sender.AppName,
		LogoURL: t.sender.LogoURL,
		Email:   view.Email,
		Data:    view.Data,
	}
	if err := parsed.ExecuteTemplate(w, "root", context); err != nil {
		return fmt.Errorf("mailer: render %s %s: %w", name, kind, err)
	}
	return nil
}

// parsed returns the template for one name and rendering.
func (t *Templates) parsed(name string, kind BodyKind) (executable, error) {
	switch kind {
	case BodyHTML:
		if parsed, ok := t.html[name]; ok {
			return parsed, nil
		}
	case BodyText:
		if parsed, ok := t.text[name]; ok {
			return parsed, nil
		}
	}
	return nil, fmt.Errorf("mailer: unknown template %q (%s); known: %s",
		name, kind, strings.Join(t.Names(), ", "))
}

// executable is the one method both template types share: the renderings are
// separate sets because their escaping differs, and this is the seam where the
// difference stops mattering.
type executable interface {
	ExecuteTemplate(w io.Writer, name string, data any) error
}
