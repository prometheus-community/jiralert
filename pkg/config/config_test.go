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
	"os"
	"path"
	"reflect"
	"testing"

	"github.com/go-kit/log"
	"github.com/stretchr/testify/require"
	yaml "gopkg.in/yaml.v3"
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
  static_labels: ["defaultlabel"]

# Receiver definitions. At least one must be defined.
receivers:
    # Must match the Alertmanager receiver name. Required.
  - name: 'jira-ab'
    # JIRA project to create the issue in. Required.
    project: AB
    # Copy all Prometheus labels into separate JIRA labels. Optional (default: false).
    add_group_labels: false
    static_labels: ["somelabel"]

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

// Generic test that loads the testConf with no errors.
func TestLoadFile(t *testing.T) {
	dir, err := os.MkdirTemp("", "test_jiralert")
	require.NoError(t, err)
	defer func() { require.NoError(t, os.RemoveAll(dir)) }()

	require.NoError(t, os.WriteFile(path.Join(dir, "config.yaml"), []byte(testConf), os.ModePerm))

	_, content, err := LoadFile(path.Join(dir, "config.yaml"), log.NewNopLogger())

	require.NoError(t, err)
	require.Equal(t, testConf, string(content))

}

// Checks if the env var substitution is happening correctly in the loaded file
func TestEnvSubstitution(t *testing.T) {

	config := "user: $(JA_USER)"
	os.Setenv("JA_USER", "user")

	content, err := substituteEnvVars([]byte(config), log.NewNopLogger())
	expected := "user: user"
	require.NoError(t, err)
	require.Equal(t, string(content), expected)

	config = "user: $(JA_MISSING)"
	_, err = substituteEnvVars([]byte(config), log.NewNopLogger())
	require.Error(t, err)

}

// A test version of the ReceiverConfig struct to create test yaml fixtures.
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

	Priority          string   `yaml:"priority,omitempty"`
	Description       string   `yaml:"description,omitempty"`
	WontFixResolution string   `yaml:"wont_fix_resolution,omitempty"`
	AddGroupLabels    bool     `yaml:"add_group_labels,omitempty"`
	StaticLabels      []string `yaml:"static_labels" json:"static_labels"`

	AutoResolve *AutoResolve `yaml:"auto_resolve" json:"auto_resolve"`

	// TODO(rporres): Add support for these.
	// Fields            map[string]interface{} `yaml:"fields,omitempty"`
	// Components        []string               `yaml:"components,omitempty"`
}

// A test version of the Config struct to create test yaml fixtures.
type testConfig struct {
	Defaults  *receiverTestConfig   `yaml:"defaults,omitempty"`
	Receivers []*receiverTestConfig `yaml:"receivers,omitempty"`
	Template  string                `yaml:"template,omitempty"`
}

// Required Config keys tests.
func TestMissingConfigKeys(t *testing.T) {
	defaultsConfig := newReceiverTestConfig(mandatoryReceiverFields(), []string{})
	receiverConfig := newReceiverTestConfig([]string{"Name"}, []string{})

	var config testConfig

	// No receivers.
	config = testConfig{Defaults: defaultsConfig, Receivers: []*receiverTestConfig{}, Template: "jiralert.tmpl"}
	configErrorTestRunner(t, config, "no receivers defined")

	// No template.
	config = testConfig{Defaults: defaultsConfig, Receivers: []*receiverTestConfig{receiverConfig}}
	configErrorTestRunner(t, config, "missing template file")
}

// Tests regarding mandatory keys.
// No tests for auth keys here. They will be handled separately.
func TestRequiredReceiverConfigKeys(t *testing.T) {
	mandatory := mandatoryReceiverFields()
	for _, test := range []struct {
		missingField string
		errorMessage string
	}{
		{"Name", "missing name for receiver"},
		{"APIURL", `missing api_url in receiver "Name"`},
		{"Project", `missing project in receiver "Name"`},
		{"IssueType", `missing issue_type in receiver "Name"`},
		{"Summary", `missing summary in receiver "Name"`},
		{"ReopenState", `missing reopen_state in receiver "Name"`},
		{"ReopenDuration", `missing reopen_duration in receiver "Name"`},
	} {

		fields := removeFromStrSlice(mandatory, test.missingField)

		// Non-empty defaults as we don't handle the empty defaults case yet.
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

// Auth keys error scenarios.
func TestAuthKeysErrors(t *testing.T) {
	mandatory := mandatoryReceiverFields()
	minimalReceiverTestConfig := newReceiverTestConfig([]string{"Name"}, []string{})

	// Test cases:
	// * missing user.
	// * missing password.
	// * specifying user and PAT auth.
	// * specifying password and PAT auth.
	// * specifying user, password and PAT auth.
	for _, test := range []struct {
		receiverTestConfigMandatoryFields []string
		errorMessage                      string
	}{
		{
			removeFromStrSlice(mandatory, "User"),
			`missing authentication in receiver "Name"`,
		},
		{
			removeFromStrSlice(mandatory, "Password"),
			`missing authentication in receiver "Name"`,
		},
		{
			append(removeFromStrSlice(mandatory, "Password"), "PersonalAccessToken"),
			"bad auth config in defaults section: user/password and PAT authentication are mutually exclusive",
		},

		{
			append(removeFromStrSlice(mandatory, "User"), "PersonalAccessToken"),
			"bad auth config in defaults section: user/password and PAT authentication are mutually exclusive",
		},
		{
			append(mandatory, "PersonalAccessToken"),
			"bad auth config in defaults section: user/password and PAT authentication are mutually exclusive",
		},
	} {

		defaultsConfig := newReceiverTestConfig(test.receiverTestConfigMandatoryFields, []string{})
		config := testConfig{
			Defaults:  defaultsConfig,
			Receivers: []*receiverTestConfig{minimalReceiverTestConfig},
			Template:  "jiralert.tmpl",
		}

		configErrorTestRunner(t, config, test.errorMessage)
	}
}

// These tests want to make sure that receiver auth always overrides defaults auth.
func TestAuthKeysOverrides(t *testing.T) {
	defaultsWithUserPassword := mandatoryReceiverFields()

	defaultsWithPAT := []string{"PersonalAccessToken"}
	for _, field := range defaultsWithUserPassword {
		if field == "User" || field == "Password" {
			continue
		}
		defaultsWithPAT = append(defaultsWithPAT, field)
	}

	// Test cases:
	// * user receiver overrides user default.
	// * password receiver overrides password default.
	// * user & password receiver overrides user & password default.
	// * PAT receiver overrides user & password default.
	// * PAT receiver overrides PAT default.
	// * user/password receiver overrides PAT default.
	for _, test := range []struct {
		userOverrideValue     string
		passwordOverrideValue string
		patOverrideValue      string // Personal Access Token override.
		userExpectedValue     string
		passwordExpectedValue string
		patExpectedValue      string
		defaultFields         []string // Fields to build the config defaults.
	}{
		{"jiraUser", "", "", "jiraUser", "Password", "", defaultsWithUserPassword},
		{"", "jiraPass", "", "User", "jiraPass", "", defaultsWithUserPassword},
		{"jiraUser", "jiraPass", "", "jiraUser", "jiraPass", "", defaultsWithUserPassword},
		{"", "", "jiraPAT", "", "", "jiraPAT", defaultsWithUserPassword},
		{"jiraUser", "jiraPass", "", "jiraUser", "jiraPass", "", defaultsWithPAT},
		{"", "", "jiraPAT", "", "", "jiraPAT", defaultsWithPAT},
	} {
		defaultsConfig := newReceiverTestConfig(test.defaultFields, []string{})
		receiverConfig := newReceiverTestConfig([]string{"Name"}, []string{})
		if test.userOverrideValue != "" {
			receiverConfig.User = test.userOverrideValue
		}
		if test.passwordOverrideValue != "" {
			receiverConfig.Password = test.passwordOverrideValue
		}
		if test.patOverrideValue != "" {
			receiverConfig.PersonalAccessToken = test.patOverrideValue
		}

		config := testConfig{
			Defaults:  defaultsConfig,
			Receivers: []*receiverTestConfig{receiverConfig},
			Template:  "jiralert.tmpl",
		}

		yamlConfig, err := yaml.Marshal(&config)
		require.NoError(t, err)

		cfg, err := Load(string(yamlConfig))
		require.NoError(t, err)

		receiver := cfg.Receivers[0]
		require.Equal(t, receiver.User, test.userExpectedValue)
		require.Equal(t, receiver.Password, Secret(test.passwordExpectedValue))
		require.Equal(t, receiver.PersonalAccessToken, Secret(test.patExpectedValue))
	}
}

// Tests regarding yaml keys overriden in the receiver config.
// No tests for auth keys here. They will be handled separately
func TestReceiverOverrides(t *testing.T) {
	fifteenHoursToDuration, err := ParseDuration("15h")
	autoResolve := AutoResolve{State: "Done"}
	require.NoError(t, err)

	// We'll override one key at a time and check the value in the receiver.
	for _, test := range []struct {
		overrideField string
		overrideValue interface{}
		expectedValue interface{}
	}{
		{"APIURL", `https://jira.redhat.com`, `https://jira.redhat.com`},
		{"Project", "APPSRE", "APPSRE"},
		{"IssueType", "Task", "Task"},
		{"Summary", "A nice summary", "A nice summary"},
		{"ReopenState", "To Do", "To Do"},
		{"ReopenDuration", "15h", &fifteenHoursToDuration},
		{"Priority", "Critical", "Critical"},
		{"Description", "A nice description", "A nice description"},
		{"WontFixResolution", "Won't Fix", "Won't Fix"},
		{"AddGroupLabels", false, false},
		{"AutoResolve", &AutoResolve{State: "Done"}, &autoResolve},
		{"StaticLabels", []string{"somelabel"}, []string{"somelabel"}},
	} {
		optionalFields := []string{"Priority", "Description", "WontFixResolution", "AddGroupLabels", "AutoResolve", "StaticLabels"}
		defaultsConfig := newReceiverTestConfig(mandatoryReceiverFields(), optionalFields)
		receiverConfig := newReceiverTestConfig([]string{"Name"}, optionalFields)

		reflect.ValueOf(receiverConfig).Elem().FieldByName(test.overrideField).
			Set(reflect.ValueOf(test.overrideValue))

		config := testConfig{
			Defaults:  defaultsConfig,
			Receivers: []*receiverTestConfig{receiverConfig},
			Template:  "jiralert.tmpl",
		}

		yamlConfig, err := yaml.Marshal(&config)
		require.NoError(t, err)

		cfg, err := Load(string(yamlConfig))
		require.NoError(t, err)

		receiver := cfg.Receivers[0]
		configValue := reflect.ValueOf(receiver).Elem().FieldByName(test.overrideField).Interface()
		require.Equal(t, configValue, test.expectedValue)
	}

}

// TODO(bwplotka, rporres). Add more tests:
//   * Tests on optional keys.
//   * Tests on unknown keys.
//   * Tests on Duration.

// Creates a receiverTestConfig struct with default values.
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
		} else if name == "AutoResolve" {
			value = reflect.ValueOf(&AutoResolve{State: "Done"})
		} else if name == "StaticLabels" {
			value = reflect.ValueOf([]string{})
		} else {
			value = reflect.ValueOf(name)
		}

		reflect.ValueOf(&r).Elem().FieldByName(name).Set(value)
	}

	return &r
}

// Creates a yaml from testConfig, Loads it checks the errors are the expected ones.
func configErrorTestRunner(t *testing.T, config testConfig, errorMessage string) {
	yamlConfig, err := yaml.Marshal(&config)
	require.NoError(t, err)

	_, err = Load(string(yamlConfig))
	require.Error(t, err)
	require.Contains(t, err.Error(), errorMessage)
}

// returns a new slice that has the element removed
func removeFromStrSlice(strSlice []string, element string) []string {
	var newStrSlice []string
	for _, value := range strSlice {
		if value != element {
			newStrSlice = append(newStrSlice, value)
		}
	}

	return newStrSlice
}

// Returns mandatory receiver fields to be used creating test config structs.
// It does not include PAT auth, those tests will be created separately.
func mandatoryReceiverFields() []string {
	return []string{"Name", "APIURL", "User", "Password", "Project",
		"IssueType", "Summary", "ReopenState", "ReopenDuration"}
}

func TestAutoResolveConfigReceiver(t *testing.T) {
	mandatory := mandatoryReceiverFields()
	minimalReceiverTestConfig := &receiverTestConfig{
		Name: "test",
		AutoResolve: &AutoResolve{
			State: "",
		},
	}

	defaultsConfig := newReceiverTestConfig(mandatory, []string{})
	config := testConfig{
		Defaults:  defaultsConfig,
		Receivers: []*receiverTestConfig{minimalReceiverTestConfig},
		Template:  "jiralert.tmpl",
	}

	configErrorTestRunner(t, config, "bad config in receiver \"test\", 'auto_resolve' was defined with empty 'state' field")

}

func TestAutoResolveConfigDefault(t *testing.T) {
	mandatory := mandatoryReceiverFields()
	minimalReceiverTestConfig := newReceiverTestConfig([]string{"Name"}, []string{"AutoResolve"})

	defaultsConfig := newReceiverTestConfig(mandatory, []string{})
	defaultsConfig.AutoResolve = &AutoResolve{
		State: "",
	}
	config := testConfig{
		Defaults:  defaultsConfig,
		Receivers: []*receiverTestConfig{minimalReceiverTestConfig},
		Template:  "jiralert.tmpl",
	}

	configErrorTestRunner(t, config, "bad config in defaults section: state cannot be empty")

}

func TestStaticLabelsConfigMerge(t *testing.T) {

	for i, test := range []struct {
		defaultValue     []string
		receiverValue    []string
		expectedElements []string
	}{
		{[]string{"defaultlabel"}, []string{"receiverlabel"}, []string{"defaultlabel", "receiverlabel"}},
		{[]string{}, []string{"receiverlabel"}, []string{"receiverlabel"}},
		{[]string{"defaultlabel"}, []string{}, []string{"defaultlabel"}},
		{[]string{}, []string{}, []string{}},
	} {
		mandatory := mandatoryReceiverFields()

		defaultsConfig := newReceiverTestConfig(mandatory, []string{})
		defaultsConfig.StaticLabels = test.defaultValue

		receiverConfig := newReceiverTestConfig([]string{"Name"}, []string{"StaticLabels"})
		receiverConfig.StaticLabels = test.receiverValue

		config := testConfig{
			Defaults:  defaultsConfig,
			Receivers: []*receiverTestConfig{receiverConfig},
			Template:  "jiralert.tmpl",
		}

		yamlConfig, err := yaml.Marshal(&config)
		require.NoError(t, err)

		cfg, err := Load(string(yamlConfig))
		require.NoError(t, err)

		receiver := cfg.Receivers[0]
		require.ElementsMatch(t, receiver.StaticLabels, test.expectedElements, "Elements should match (failing index: %v)", i)
	}
}
