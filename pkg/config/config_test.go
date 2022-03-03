// Copyright 2017 The Prometheus Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package config

import (
	"io/ioutil"
	"os"
	"path"
	"reflect"
	"testing"

	"github.com/go-kit/kit/log"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v2"
)

const testConf = `
# Global defaults, applied to all receivers where not explicitly overridden. Optional.
defaults:
  # API access fields.
  api_url: https://jiralert.atlassian.net
  user: jiralert
  password: 'JIRAlert'

  # The type of JIRA issue to create. Required.
  issue_type: Bug
  # Issue priority. Optional.
  priority: Critical
  # Go template invocation for generating the summary. Required.
  summary: '{{ template "jira.summary" . }}'
  # Go template invocation for generating the description. Optional.
  description: '{{ template "jira.description" . }}'
  # State to transition into when reopening a closed issue. Required.
  reopen_state: "To Do"
  # Do not reopen issues with this resolution. Optional.
  wont_fix_resolution: "Won't Fix"
  # Amount of time after being closed that an issue should be reopened, after which, a new issue is created.
  # Optional (default: always reopen)
  reopen_duration: 0h

# Receiver definitions. At least one must be defined.
receivers:
    # Must match the Alertmanager receiver name. Required.
  - name: 'jira-ab'
    # JIRA project to create the issue in. Required.
    project: AB
    # Copy all Prometheus labels into separate JIRA labels. Optional (default: false).
    add_group_labels: false

  - name: 'jira-xy'
    project: XY
    # Overrides default.
    issue_type: Task
    # JIRA components. Optional.
    components: [ 'Operations' ]
    # Standard or custom field values to set on created issue. Optional.
    #
    # See https://developer.atlassian.com/server/jira/platform/jira-rest-api-examples/#setting-custom-field-data-for-other-field-types for further examples.
    fields:
      # TextField
      customfield_10001: "Random text"
      # SelectList
      customfield_10002: { "value": "red" }
      # MultiSelect
      customfield_10003: [{"value": "red" }, {"value": "blue" }, {"value": "green" }]

# File containing template definitions. Required.
template: jiralert.tmpl
`

// Generic test that loads the testConf with no errors
func TestLoadFile(t *testing.T) {
	dir, err := ioutil.TempDir("", "test_jiralert")
	require.NoError(t, err)
	defer func() { require.NoError(t, os.RemoveAll(dir)) }()

	require.NoError(t, ioutil.WriteFile(path.Join(dir, "config.yaml"), []byte(testConf), os.ModePerm))

	_, content, err := LoadFile(path.Join(dir, "config.yaml"), log.NewNopLogger())

	require.NoError(t, err)
	require.Equal(t, testConf, string(content))

}

// returns mandatory receiver fields to be used creating test config structs
// it does not include PAT auth, those tests will be created separately
func mandatoryReceiverFields() []string {
	return []string{"Name", "APIURL", "User", "Password", "Project",
		"IssueType", "Summary", "ReopenState", "ReopenDuration"}
}

// A test version of the ReceiverConfig struct to create test yaml fixtures
type receiverTestConfig struct {
	Name                string `yaml:"name,omitempty"`
	APIURL              string `yaml:"api_url,omitempty"`
	User                string `yaml:"user,omitempty"`
	Password            string `yaml:"password,omitempty"`
	PersonalAccessToken string `yaml:"personal_access_token,omitempty"`
	Project             string `yaml:"project,omitempty"`
	IssueType           string `yaml:"issue_type,omitempty"`
	Summary             string `yaml:"summary,omitempty"`
	ReopenState         string `yaml:"reopen_state,omitempty"`
	ReopenDuration      string `yaml:"reopen_duration,omitempty"`

	Priority          string `yaml:"priority,omitempty"`
	Description       string `yaml:"description,omitempty"`
	WontFixResolution string `yaml:"wont_fix_resolution,omitempty"`
	AddGroupLabels    bool   `yaml:"add_group_labels,omitempty"`

	// TODO(rporres): Add support for these
	// Fields            map[string]interface{} `yaml:"fields,omitempty"`
	// Components        []string               `yaml:"components,omitempty"`
}

// A test version of the Config struct to create test yaml fixtures
type testConfig struct {
	Defaults  *receiverTestConfig   `yaml:"defaults,omitempty"`
	Receivers []*receiverTestConfig `yaml:"receivers,omitempty"`
	Template  string                `yaml:"template,omitempty"`
}

// create a receiverTestConfig struct with default values
func newReceiverTestConfig(mandatory []string, optional []string) *receiverTestConfig {
	r := receiverTestConfig{}

	for _, name := range mandatory {
		var value reflect.Value
		if name == "APIURL" {
			value = reflect.ValueOf("https://jiralert.atlassian.net")
		} else if name == "ReopenDuration" {
			value = reflect.ValueOf("30d")
		} else {
			value = reflect.ValueOf(name)
		}

		reflect.ValueOf(&r).Elem().FieldByName(name).Set(value)
	}

	for _, name := range optional {
		var value reflect.Value
		if name == "AddGroupLabels" {
			value = reflect.ValueOf(true)
		} else {
			value = reflect.ValueOf(name)
		}

		reflect.ValueOf(&r).Elem().FieldByName(name).Set(value)
	}

	return &r
}

// Required Config keys tests
func TestMissingConfigKeys(t *testing.T) {
	defaultsConfig := newReceiverTestConfig(mandatoryReceiverFields(), []string{})
	receiverConfig := newReceiverTestConfig([]string{"Name"}, []string{})
	template := "jiralert.tmpl"

	var config testConfig

	// No receivers
	config = testConfig{Defaults: defaultsConfig, Receivers: []*receiverTestConfig{}, Template: template}
	configErrorTestRunner(t, config, "no receivers defined")

	// No template
	config = testConfig{Defaults: defaultsConfig, Receivers: []*receiverTestConfig{receiverConfig}}
	configErrorTestRunner(t, config, "missing template file")
}

// Creates a yaml from testConfig and checks the errors while loading it
func configErrorTestRunner(t *testing.T, config testConfig, errorMessage string) {
	var yamlConfig []byte
	var err error

	yamlConfig, err = yaml.Marshal(&config)
	require.NoError(t, err)

	_, err = Load(string(yamlConfig))
	require.Error(t, err)
	require.Contains(t, err.Error(), errorMessage)
}

// Tests regarding mandatory keys
func TestRequiredReceiverConfigKeys(t *testing.T) {
	type testCase struct {
		missingField string
		errorMessage string
	}

	testTable := []testCase{
		{"Name", "missing name for receiver"},
		{"APIURL", `missing api_url in receiver "Name"`},
		{"User", `missing user in receiver "Name"`},
		{"Password", `missing password in receiver "Name"`},
		{"Project", `missing project in receiver "Name"`},
		{"IssueType", `missing issue_type in receiver "Name"`},
		{"Summary", `missing summary in receiver "Name"`},
		{"ReopenState", `missing reopen_state in receiver "Name"`},
		{"ReopenDuration", `missing reopen_duration in receiver "Name"`},
	}

	for _, test := range testTable {
		var fields []string
		for _, value := range mandatoryReceiverFields() {
			if value != test.missingField {
				fields = append(fields, value)
			}
		}

		// non-empty defaults as we don't handle the empty defaults case yet
		defaultsConfig := newReceiverTestConfig([]string{}, []string{"Priority"})
		receiverConfig := newReceiverTestConfig(fields, []string{})
		config := testConfig{
			Defaults:  defaultsConfig,
			Receivers: []*receiverTestConfig{receiverConfig},
			Template:  "jiratemplate.tmpl",
		}
		configErrorTestRunner(t, config, test.errorMessage)
	}

}

// Tests regarding yaml keys overriden in the receiver config
func TestReceiverOverrides(t *testing.T) {
	type testCase struct {
		overrideField string
		overrideValue interface{}
		expectedValue interface{}
	}

	fifteenHoursToDuration, err := ParseDuration("15h")
	require.NoError(t, err)

	testTable := []testCase{
		{"APIURL", `https://jira.redhat.com`, `https://jira.redhat.com`},
		{"User", "jirauser", "jirauser"},
		{"Password", "jirapassword", Secret("jirapassword")},
		{"Project", "APPSRE", "APPSRE"},
		{"IssueType", "Task", "Task"},
		{"Summary", "A nice summary", "A nice summary"},
		{"ReopenState", "To Do", "To Do"},
		{"ReopenDuration", "15h", &fifteenHoursToDuration},
		{"Priority", "Critical", "Critical"},
		{"Description", "A nice description", "A nice description"},
		{"WontFixResolution", "Won't Fix", "Won't Fix"},
		{"AddGroupLabels", false, false},
	}

	for _, test := range testTable {
		optionalFields := []string{"Priority", "Description", "WontFixResolution", "AddGroupLabels"}
		defaultsConfig := newReceiverTestConfig(mandatoryReceiverFields(), optionalFields)
		receiverConfig := newReceiverTestConfig([]string{"Name"}, optionalFields)

		reflect.ValueOf(receiverConfig).Elem().FieldByName(test.overrideField).
			Set(reflect.ValueOf(test.overrideValue))

		config := testConfig{
			Defaults:  defaultsConfig,
			Receivers: []*receiverTestConfig{receiverConfig},
			Template:  "jiralert.tmpl",
		}

		var yamlConfig []byte
		var err error
		var cfg *Config

		yamlConfig, err = yaml.Marshal(&config)
		require.NoError(t, err)

		cfg, err = Load(string(yamlConfig))
		require.NoError(t, err)

		receiver := cfg.Receivers[0]
		configValue := reflect.ValueOf(receiver).Elem().FieldByName(test.overrideField).Interface()

		require.Equal(t, configValue, test.expectedValue)
	}

}

// TODO(bwplotka, rporres): Add more tests
//   * Tests on optional keys
//   * Tests on unknown keys
