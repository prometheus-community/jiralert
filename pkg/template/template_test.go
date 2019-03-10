package template

import (
	"io/ioutil"
	"jiralert/alertmanager"
	"os"
	"path"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTemplate_Execute_NoFile(t *testing.T) {
	tmpl, err := LoadTemplate("")
	require.NoError(t, err)

	require.NoError(t, tmpl.Err())
	require.Equal(t, "test", tmpl.Execute("test", nil))
	require.NoError(t, tmpl.Err())
}

func TestTemplate_Execute_EmptyFile(t *testing.T) {
	dir, err := ioutil.TempDir("", "jiralert_template-test")
	require.NoError(t, err)
	defer func() {
		require.NoError(t, os.RemoveAll(dir))
	}()

	filename := path.Join(dir, "file.something")
	require.NoError(t, ioutil.WriteFile(filename, nil, os.ModePerm))

	tmpl, err := LoadTemplate(filename)
	require.NoError(t, err)

	require.NoError(t, tmpl.Err())
	require.Equal(t, "test", tmpl.Execute("test", nil))
	require.NoError(t, tmpl.Err())
}

func TestTemplate_Execute_FileWithVariables(t *testing.T) {
	const (
		tmplInput = `
{{ define "jira.summary" }}[{{ .Status | toUpper }}{{ if eq .Status "firing" }}:{{ .Alerts.Firing | len }}{{ end }}] {{ .GroupLabels.SortedPairs.Values | join " " }} {{ if gt (len .CommonLabels) (len .GroupLabels) }}({{ with .CommonLabels.Remove .GroupLabels.Names }}{{ .Values | join " " }}{{ end }}){{ end }}{{ end }}

{{ define "jira.description" }}{{ range .Alerts.Firing }}Labels:
{{ range .Labels.SortedPairs }} - {{ .Name }} = {{ .Value }}
{{ end }}
Annotations:
{{ range .Annotations.SortedPairs }} - {{ .Name }} = {{ .Value }}
{{ end }}
Source: {{ .GeneratorURL }}
{{ end }}{{ end }}
`
		text = `
{{ template "jira.summary" . }}
{{ template "jira.description" . }}
`
		expected = `
[FIRING:2] 1 2 
Labels:
 - a = 1
 - b = 2
 - new = 3

Annotations:

Source: gen url
Labels:
 - a = 1
 - b = 2
 - new = 4

Annotations:

Source: gen url

`
	)

	dir, err := ioutil.TempDir("", "jiralert_template-test")
	require.NoError(t, err)
	defer func() {
		require.NoError(t, os.RemoveAll(dir))
	}()

	data := &alertmanager.Data{
		Alerts: alertmanager.Alerts{
			{
				Status:       "firing",
				GeneratorURL: "gen url",
				Labels: alertmanager.KV{
					"b":   "2",
					"a":   "1",
					"new": "3",
				},
			},
			{
				Status:       "firing",
				GeneratorURL: "gen url",
				Labels: alertmanager.KV{
					"b":   "2",
					"a":   "1",
					"new": "4",
				},
			},
		},
		Status:      "firing",
		ExternalURL: "external-url",
		GroupLabels: alertmanager.KV{
			"b": "2",
			"a": "1",
		},
	}

	filename := path.Join(dir, "template-file.something")
	require.NoError(t, ioutil.WriteFile(filename, []byte(tmplInput), os.ModePerm))

	tmpl, err := LoadTemplate(filename)
	require.NoError(t, err)

	require.NoError(t, tmpl.Err())
	require.Equal(t, expected, tmpl.Execute(text, data))
	require.NoError(t, tmpl.Err())
}
