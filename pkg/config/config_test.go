package config

import (
	"github.com/go-kit/kit/log"
	"github.com/stretchr/testify/require"
	"io/ioutil"
	"os"
	"path"
	"testing"
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

func TestLoadFile(t *testing.T) {
	dir, err := ioutil.TempDir("", "test_jiralert")
	require.NoError(t, err)
	defer func() { require.NoError(t, os.RemoveAll(dir)) }()

	require.NoError(t, ioutil.WriteFile(path.Join(dir, "config.yaml"), []byte(testConf), os.ModePerm))

	_, content, err := LoadFile(path.Join(dir, "config.yaml"), log.NewNopLogger())

	require.NoError(t, err)
	require.Equal(t, testConf, string(content))

	// TODO(bwplotka): Add proper test cases on config struct.
}
