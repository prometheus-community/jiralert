package template

import (
	"bytes"
	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"regexp"
	"strings"
	"text/template"
)

// Template wraps a text template and error, to make it easier to execute multiple templates and only check for errors
// once at the end (assuming one is only interested in the first error, which is usually the case).
type Template struct {
	tmpl *template.Template
	err  error
}

var funcs = template.FuncMap{
	"toUpper": strings.ToUpper,
	"toLower": strings.ToLower,
	"title":   strings.Title,
	// join is equal to strings.Join but inverts the argument order
	// for easier pipelining in templates.
	"join": func(sep string, s []string) string {
		return strings.Join(s, sep)
	},
	"reReplaceAll": func(pattern, repl, text string) string {
		re := regexp.MustCompile(pattern)
		return re.ReplaceAllString(text, repl)
	},
}

// LoadTemplate reads and parses all templates defined in the given file and constructs a jiralert.Template.
func LoadTemplate(path string, logger log.Logger) (*Template, error) {
	level.Debug(logger).Log("msg", "loading templates", "path", path)
	tmpl, err := template.New("").Option("missingkey=zero").Funcs(funcs).ParseFiles(path)
	if err != nil {
		return nil, err
	}
	return &Template{tmpl: tmpl}, nil
}

func (t *Template) Err() error {
	return t.err
}

// Execute parses the provided text (or returns it unchanged if not a Go template), associates it with the templates
// defined in t.tmpl (so they may be referenced and used) and applies the resulting template to the specified data
// object, returning the output as a string.
func (t *Template) Execute(text string, data interface{}, logger log.Logger) string {
	level.Debug(logger).Log("msg", "executing template", "template", text)
	if !strings.Contains(text, "{{") {
		level.Debug(logger).Log("msg", "  returning unchanged")
		return text
	}

	if t.err != nil {
		return ""
	}
	var tmpl *template.Template
	tmpl, t.err = t.tmpl.Clone()
	if t.err != nil {
		return ""
	}
	tmpl, t.err = tmpl.New("").Parse(text)
	if t.err != nil {
		level.Warn(logger).Log("msg", "failed to parse template", "template", text)
		return ""
	}
	var buf bytes.Buffer
	t.err = tmpl.Execute(&buf, data)
	ret := buf.String()
	level.Debug(logger).Log("msg", "  template output", "output", ret)
	return ret
}
