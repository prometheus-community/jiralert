package jiralert

import (
	"bytes"
	"regexp"
	"strings"
	"text/template"

	log "github.com/golang/glog"
)

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

func LoadTemplate(path string) (*Template, error) {
	log.V(1).Infof("Loading templates from %q", path)
	tmpl, err := template.New("").Option("missingkey=zero").Funcs(funcs).ParseFiles(path)
	if err != nil {
		return nil, err
	}
	return &Template{tmpl: tmpl}, nil
}

func (t *Template) Execute(text string, data interface{}) string {
	log.V(2).Infof("Executing template %q...", text)
	if !strings.Contains(text, "{{") {
		log.V(2).Infof("  returning unchanged.")
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
		log.V(2).Infof("  parse failed.")
		return ""
	}
	var buf bytes.Buffer
	t.err = tmpl.Execute(&buf, data)
	ret := buf.String()
	log.V(2).Infof("  returning %q.", ret)
	return ret
}
