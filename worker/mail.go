package worker

import (
	"bytes"
	"cmp"
	"crypto/rand"
	"embed"
	"errors"
	"fmt"
	htmltemplate "html/template"
	"io"
	"io/fs"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"os"
	"regexp"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/mizuchilabs/inkan/license"
)

const defaultAccent = "#2f3e8f"

//go:embed templates
var builtIn embed.FS

var (
	defaultTemplates = func() *Templates {
		t, err := parseTemplates(builtIn, "templates")
		if err != nil {
			panic(err)
		}
		return t
	}()
	hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	// An https link or an email address. The space class is spelled out so
	// the Cloudflare worker can use the very same expression.
	linkable = regexp.MustCompile(`https://[^ \t\n\f\r<>"]+|[\w.+-]+@[\w-]+(?:\.[\w-]+)+`)
	// The characters html/template escapes, so linked reads like the rest.
	escaper = strings.NewReplacer(
		"&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&#34;", "'", "&#39;", "+", "&#43;",
	)
)

// Brand is how the built in key mail looks. Every field may stay empty.
type Brand struct {
	// Name is how the product is called in the mail, the key prefix when empty.
	Name string
	// Logo is the https url of an image. It is shown 32 pixels high on the
	// dark header, so it has to read on a dark background.
	Logo string
	// Accent is a color like #2f3e8f.
	Accent string
	// Footer is one line under the key, like where to get help. Links and
	// email addresses in it can be clicked.
	Footer string
	// Company is one line at the very end, for what a business has to state
	// in its mail, like its legal name, address and register number. Links
	// and email addresses in it can be clicked.
	Company string
}

// Templates are the subject and the bodies of the key mail.
type Templates struct {
	subject *template.Template
	text    *template.Template
	html    *htmltemplate.Template // nil sends plain text only
}

// MailData is what a template can use.
type MailData struct {
	Key       string
	App       string // the key prefix
	Name      string // of the product, the key prefix without a brand name
	Plan      string
	Email     string
	Expires   string            // like 15 January 2027, empty for a key that never expires
	ExpiresAt time.Time         // zero for a key that never expires
	Meta      map[string]string // extra claims of the key, like seats
	Logo      string
	Accent    string
	Footer    string
	Company   string
	Shades    Shades
}

// Shades are the colors the built in mail works out from the accent, so a
// pale or a dark accent stays readable in the light and the dark mail.
type Shades struct {
	Text           string // the accent as text on the light mail
	TextDark       string // and on the dark one
	Tint           string // background of the key in the light mail
	TintBorder     string
	TintDark       string // and in the dark one
	TintBorderDark string
}

// LoadTemplates reads the templates of the key mail from a directory:
// subject.txt, body.txt and body.html. They are Go templates over [MailData].
// A missing file falls back to the built in one. A directory with a body.txt
// and no body.html sends plain text only.
//
// body.html also has the function linked, {{linked .Footer .Shades.Text}}
// turns the links and email addresses of a text into links of that color.
func LoadTemplates(dir string) (*Templates, error) {
	t, err := parseTemplates(os.DirFS(dir), ".")
	if err != nil {
		return nil, fmt.Errorf("mail templates: %w", err)
	}
	if t.subject == nil && t.text == nil && t.html == nil {
		return nil, fmt.Errorf("mail templates: %s has no subject.txt, body.txt or body.html", dir)
	}
	// Only a new subject keeps both built in bodies.
	if t.text == nil && t.html == nil {
		t.html = defaultTemplates.html
	}
	t.subject = cmp.Or(t.subject, defaultTemplates.subject)
	t.text = cmp.Or(t.text, defaultTemplates.text)
	return t, nil
}

func parseTemplates(fsys fs.FS, dir string) (*Templates, error) {
	read := func(name string) (string, bool, error) {
		b, err := fs.ReadFile(fsys, strings.TrimPrefix(dir+"/"+name, "./"))
		if errors.Is(err, fs.ErrNotExist) {
			return "", false, nil
		}
		return string(b), err == nil, err
	}
	var t Templates
	for _, name := range []string{"subject.txt", "body.txt"} {
		src, ok, err := read(name)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		parsed, err := template.New(name).Parse(src)
		if err != nil {
			return nil, err
		}
		if name == "subject.txt" {
			t.subject = parsed
		} else {
			t.text = parsed
		}
	}
	src, ok, err := read("body.html")
	if err != nil {
		return nil, err
	}
	if ok {
		funcs := htmltemplate.FuncMap{"linked": linked}
		if t.html, err = htmltemplate.New("body.html").Funcs(funcs).Parse(src); err != nil {
			return nil, err
		}
	}
	return &t, nil
}

// Render fills the templates for one key, to preview the mail or to send it
// some other way than [SMTP]. A nil t renders the built in mail. html is
// empty for templates that are plain text only.
func (t *Templates) Render(
	key string,
	claims license.Claims,
	brand Brand,
) (subject, text, html string, err error) {
	t = cmp.Or(t, defaultTemplates)
	data := MailData{
		Key:     key,
		App:     claims.App,
		Name:    brand.Name,
		Plan:    claims.Plan,
		Email:   claims.Email,
		Meta:    claims.Meta,
		Accent:  defaultAccent,
		Footer:  brand.Footer,
		Company: brand.Company,
	}
	if data.Name == "" {
		data.Name = claims.App
	}
	if claims.ExpiresAt != 0 {
		data.ExpiresAt = time.Unix(claims.ExpiresAt, 0).UTC()
		data.Expires = data.ExpiresAt.Format("2 January 2006")
	}
	// These end up in the markup, so only what is known to be harmless gets in.
	if hexColor.MatchString(brand.Accent) {
		data.Accent = brand.Accent
	}
	if strings.HasPrefix(brand.Logo, "https://") {
		data.Logo = brand.Logo
	}
	data.Shades = shades(data.Accent)

	var s, b, h bytes.Buffer
	if err := t.subject.Execute(&s, data); err != nil {
		return "", "", "", fmt.Errorf("mail subject: %w", err)
	}
	if err := t.text.Execute(&b, data); err != nil {
		return "", "", "", fmt.Errorf("mail body: %w", err)
	}
	if t.html != nil {
		if err := t.html.Execute(&h, data); err != nil {
			return "", "", "", fmt.Errorf("mail body: %w", err)
		}
	}
	// A subject is one line, whatever the template or the product name hold.
	return strings.Join(strings.Fields(s.String()), " "), b.String(), h.String(), nil
}

// shades only mixes and compares in whole numbers, so the Cloudflare worker
// gets the very same colors.
func shades(accent string) Shades {
	n, _ := strconv.ParseUint(accent[1:], 16, 32)
	c := [3]int{int(n >> 16), int(n >> 8 & 0xff), int(n & 0xff)}
	white, ink, card := [3]int{255, 255, 255}, [3]int{28, 31, 46}, [3]int{26, 29, 41}
	// share is how many percent of to go into the mix.
	mix := func(to [3]int, share int) string {
		var out [3]int
		for i := range out {
			out[i] = (c[i]*(100-share) + to[i]*share + 50) / 100
		}
		return fmt.Sprintf("#%02x%02x%02x", out[0], out[1], out[2])
	}
	// How bright the eye takes the color to be, 0 to 255.
	bright := (299*c[0] + 587*c[1] + 114*c[2]) / 1000

	s := Shades{
		Text:           accent,
		TextDark:       accent,
		Tint:           mix(white, 92),
		TintBorder:     mix(white, 74),
		TintDark:       mix(card, 84),
		TintBorderDark: mix(card, 60),
	}
	if bright > 110 {
		s.Text = mix(ink, 50)
	}
	if bright < 150 {
		s.TextDark = mix(white, 50)
	}
	return s
}

// linked escapes a text and turns its https links and email addresses into
// links of the given color.
func linked(text, color string) htmltemplate.HTML {
	var b strings.Builder
	last := 0
	for _, at := range linkable.FindAllStringIndex(text, -1) {
		// The full stop after a link is the end of the sentence.
		match := strings.TrimRight(text[at[0]:at[1]], ".,;:!?)")
		href, label := "mailto:"+match, match
		if rest, ok := strings.CutPrefix(match, "https://"); ok {
			href, label = match, strings.TrimSuffix(rest, "/")
		}
		b.WriteString(escaper.Replace(text[last:at[0]]))
		fmt.Fprintf(&b, `<a href="%s" class="accent" style="color:%s;">%s</a>`,
			escaper.Replace(href), escaper.Replace(color), escaper.Replace(label))
		last = at[0] + len(match)
	}
	b.WriteString(escaper.Replace(text[last:]))
	return htmltemplate.HTML(b.String()) //nolint:gosec // every piece went through the escaper
}

// message renders the whole mail, headers included.
func (t *Templates) message(
	from, to *mail.Address,
	key string,
	claims license.Claims,
	brand Brand,
) ([]byte, error) {
	subject, text, html, err := t.Render(key, claims, brand)
	if err != nil {
		return nil, err
	}

	_, domain, _ := strings.Cut(from.Address, "@")
	var b bytes.Buffer
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", to)
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", subject))
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	fmt.Fprintf(&b, "Message-ID: <%s@%s>\r\n", rand.Text(), domain)
	b.WriteString("MIME-Version: 1.0\r\n")

	if html == "" {
		b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
		b.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")
		if err := writeQuoted(&b, []byte(text)); err != nil {
			return nil, err
		}
		return b.Bytes(), nil
	}
	parts := multipart.NewWriter(&b)
	fmt.Fprintf(&b, "Content-Type: multipart/alternative; boundary=%s\r\n\r\n", parts.Boundary())
	// The last part is the one a mail client prefers.
	for _, part := range []struct {
		kind string
		body []byte
	}{{"text/plain", []byte(text)}, {"text/html", []byte(html)}} {
		w, err := parts.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {part.kind + "; charset=utf-8"},
			"Content-Transfer-Encoding": {"quoted-printable"},
		})
		if err != nil {
			return nil, err
		}
		if err := writeQuoted(w, part.body); err != nil {
			return nil, err
		}
	}
	if err := parts.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func writeQuoted(w io.Writer, body []byte) error {
	q := quotedprintable.NewWriter(w)
	if _, err := q.Write(body); err != nil {
		return err
	}
	return q.Close()
}

// Sample makes up a key to show the mail with. It is signed with a key made
// for the occasion, so it looks real and works nowhere.
func Sample(app, email, plan string) (string, license.Claims, error) {
	privateKey, _, err := license.GenerateKey()
	if err != nil {
		return "", license.Claims{}, err
	}
	priv, err := license.ParsePrivateKey(privateKey)
	if err != nil {
		return "", license.Claims{}, err
	}
	claims := license.Claims{
		App:       app,
		Plan:      plan,
		Email:     email,
		ExpiresAt: time.Now().AddDate(1, 0, 0).Unix(),
		ID:        "sub_sample",
	}
	key, err := license.Issue(app, claims, priv)
	return key, claims, err
}
