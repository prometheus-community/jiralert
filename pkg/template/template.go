package template

import (
	"bytes"
	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/pkg/errors"
	"regexp"
	"strings"
	"text/template"
)

type Template struct {
	tmpl   *template.Template
	logger log.Logger
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
	return &Template{tmpl: tmpl, logger: logger}, nil
}

func SimpleTemplate() *Template {
	return &Template{logger: log.NewNopLogger(), tmpl: template.New("").Option("missingkey=zero").Funcs(funcs)}
}

// Execute parses the provided text (or returns it unchanged if not a Go template), associates it with the templates
// defined in t.tmpl (so they may be referenced and used) and applies the resulting template to the specified data
// object, returning the output as a string .
func (t *Template) Execute(text string, data interface{}) (string, error) {
	level.Debug(t.logger).Log("msg", "executing template", "template", text)
	if !strings.Contains(text, "{{") {
		level.Debug(t.logger).Log("msg", "returning unchanged")
		return text, nil
	}

	tmpl, err := t.tmpl.Clone()
	if err != nil {
		// There is literally no return flow in Clone that returns error.
		return "", errors.Wrap(err, "parse clone tmpl")
	}
	tmpl, err = tmpl.New("").Parse(text)
	if err != nil {
		return "", errors.Wrapf(err, "parse template %s", text)
	}
	var buf bytes.Buffer

	if err = tmpl.Execute(&buf, data); err != nil {
		return "", errors.Wrapf(err, "execute template %s", text)
	}
	ret := buf.String()
	level.Debug(t.logger).Log("msg", "template output", "output", ret)
	return ret, nil
}
