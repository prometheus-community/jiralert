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
package notify

import (
	"fmt"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/andygrunwald/go-jira"

	"github.com/trivago/tgo/tcontainer"

	"github.com/go-kit/log"
	"github.com/pkg/errors"
	"github.com/prometheus-community/jiralert/pkg/alertmanager"
	"github.com/prometheus-community/jiralert/pkg/config"
	"github.com/prometheus-community/jiralert/pkg/template"
	"github.com/stretchr/testify/require"
)

func TestToGroupTicketLabel(t *testing.T) {
	require.Equal(t, `JIRALERT{9897cb21a3d1ba47d2aab501ce9bc60b74bf65e26658f8e34a7fc81705e6b6eadfe6ad8edfe7c68142b3fe10f2c89127bd85e5f3687fe6b9ff1eff4b3f71dd49}`, toGroupTicketLabel(alertmanager.KV{"a": "B", "C": "d"}, true))
	require.Equal(t, `ALERT{C="d",a="B"}`, toGroupTicketLabel(alertmanager.KV{"a": "B", "C": "d"}, false))
}

type fakeJira struct {
	// Key = ID for simplification.
	issuesByKey map[string]*jira.Issue
	keysByQuery map[string][]string

	transitionsByID map[string]jira.Transition
}

func newTestFakeJira() *fakeJira {
	return &fakeJira{
		issuesByKey:     map[string]*jira.Issue{},
		transitionsByID: map[string]jira.Transition{"1234": {ID: "1234", Name: "Done"}},
		keysByQuery:     map[string][]string{},
	}
}

func (f *fakeJira) Search(jql string, options *jira.SearchOptions) ([]jira.Issue, *jira.Response, error) {
	var issues []jira.Issue
	for _, key := range f.keysByQuery[jql] {
		issue := jira.Issue{Key: key, Fields: &jira.IssueFields{}}
		for _, field := range options.Fields {
			switch field {
			case "summary":
				issue.Fields.Summary = f.issuesByKey[key].Fields.Summary
			case "resolution":
				if f.issuesByKey[key].Fields.Resolution == nil {
					continue
				}
				issue.Fields.Resolution = &jira.Resolution{
					Name: f.issuesByKey[key].Fields.Resolution.Name,
				}
			case "resolutiondate":
				issue.Fields.Resolutiondate = f.issuesByKey[key].Fields.Resolutiondate
			case "status":
				issue.Fields.Status = &jira.Status{
					StatusCategory: f.issuesByKey[key].Fields.Status.StatusCategory,
				}
			}
		}
		issues = append(issues, issue)
	}

	// We assume query 'order by resolutiondate desc' so let's sort by resolution date if any.
	sort.Slice(issues, func(i, j int) bool {
		return time.Time(issues[i].Fields.Resolutiondate).After(time.Time(issues[j].Fields.Resolutiondate))
	})

	if len(issues) > options.MaxResults {
		issues = issues[:options.MaxResults]
	}
	return issues, nil, nil
}

func (f *fakeJira) GetTransitions(_ string) ([]jira.Transition, *jira.Response, error) {
	var trs []jira.Transition
	for _, tr := range f.transitionsByID {
		trs = append(trs, tr)
	}
	return trs, nil, nil
}

func (f *fakeJira) Create(issue *jira.Issue) (*jira.Issue, *jira.Response, error) {
	issue.Key = fmt.Sprintf("%d", len(f.issuesByKey)+1)
	issue.ID = issue.Key
	issue.Fields.Status = &jira.Status{
		StatusCategory: jira.StatusCategory{Key: "NotDone"},
	}
	f.issuesByKey[issue.Key] = issue

	// Assuming single label.
	query := fmt.Sprintf(
		"project in('%s') and labels=%q order by resolutiondate desc",
		issue.Fields.Project.Key,
		issue.Fields.Labels[0],
	)
	f.keysByQuery[query] = append(f.keysByQuery[query], issue.Key)

	return issue, nil, nil
}

func (f *fakeJira) UpdateWithOptions(old *jira.Issue, _ *jira.UpdateQueryOptions) (*jira.Issue, *jira.Response, error) {
	issue, ok := f.issuesByKey[old.Key]
	if !ok {
		return nil, nil, errors.Errorf("no such issue %s", old.Key)
	}

	if old.Fields.Summary != "" {
		issue.Fields.Summary = old.Fields.Summary
	}

	if old.Fields.Description != "" {
		issue.Fields.Description = old.Fields.Description
	}

	f.issuesByKey[issue.Key] = issue
	return issue, nil, nil
}

func (f *fakeJira) DoTransition(ticketID, transitionID string) (*jira.Response, error) {
	issue, ok := f.issuesByKey[ticketID]
	if !ok {
		return nil, errors.Errorf("no such issue %s", ticketID)
	}

	tr, ok := f.transitionsByID[transitionID]
	if !ok {
		return nil, errors.Errorf("no such transition %s", tr.ID)
	}

	issue.Fields.Status.StatusCategory.Key = tr.Name

	f.issuesByKey[issue.Key] = issue

	return nil, nil
}

func testReceiverConfig1() *config.ReceiverConfig {
	reopen := config.Duration(1 * time.Hour)
	return &config.ReceiverConfig{
		Project:           "abc",
		Summary:           `[{{ .Status | toUpper }}{{ if eq .Status "firing" }}:{{ .Alerts.Firing | len }}{{ end }}] {{ .GroupLabels.SortedPairs.Values | join " " }} {{ if gt (len .CommonLabels) (len .GroupLabels) }}({{ with .CommonLabels.Remove .GroupLabels.Names }}{{ .Values | join " " }}{{ end }}){{ end }}`,
		ReopenDuration:    &reopen,
		ReopenState:       "reopened",
		WontFixResolution: "won't-fix",
	}
}

func testReceiverConfig2() *config.ReceiverConfig {
	reopen := config.Duration(1 * time.Hour)
	return &config.ReceiverConfig{
		Project:           "abc",
		Summary:           `[{{ .Status | toUpper }}{{ if eq .Status "firing" }}:{{ .Alerts.Firing | len }}{{ end }}] {{ .GroupLabels.SortedPairs.Values | join " " }} {{ if gt (len .CommonLabels) (len .GroupLabels) }}({{ with .CommonLabels.Remove .GroupLabels.Names }}{{ .Values | join " " }}{{ end }}){{ end }}`,
		ReopenDuration:    &reopen,
		ReopenState:       "reopened",
		Description:       `{{ .Alerts.Firing | len }}`,
		WontFixResolution: "won't-fix",
	}
}

func testReceiverConfigAutoResolve() *config.ReceiverConfig {
	reopen := config.Duration(1 * time.Hour)
	autoResolve := config.AutoResolve{State: "Done"}
	return &config.ReceiverConfig{
		Project:           "abc",
		Summary:           `[{{ .Status | toUpper }}{{ if eq .Status "firing" }}:{{ .Alerts.Firing | len }}{{ end }}] {{ .GroupLabels.SortedPairs.Values | join " " }} {{ if gt (len .CommonLabels) (len .GroupLabels) }}({{ with .CommonLabels.Remove .GroupLabels.Names }}{{ .Values | join " " }}{{ end }}){{ end }}`,
		ReopenDuration:    &reopen,
		ReopenState:       "reopened",
		WontFixResolution: "won't-fix",
		AutoResolve:       &autoResolve,
	}
}

func testReceiverConfigWithStaticLabels() *config.ReceiverConfig {
	reopen := config.Duration(1 * time.Hour)
	return &config.ReceiverConfig{
		Project:           "abc",
		Summary:           `[{{ .Status | toUpper }}{{ if eq .Status "firing" }}:{{ .Alerts.Firing | len }}{{ end }}] {{ .GroupLabels.SortedPairs.Values | join " " }} {{ if gt (len .CommonLabels) (len .GroupLabels) }}({{ with .CommonLabels.Remove .GroupLabels.Names }}{{ .Values | join " " }}{{ end }}){{ end }}`,
		ReopenDuration:    &reopen,
		ReopenState:       "reopened",
		WontFixResolution: "won't-fix",
		StaticLabels:      []string{"somelabel"},
	}
}

func TestNotify_JIRAInteraction(t *testing.T) {
	testNowTime := time.Now()

	for _, tcase := range []struct {
		name               string
		inputAlert         *alertmanager.Data
		inputConfig        *config.ReceiverConfig
		initJira           func(t *testing.T) *fakeJira
		expectedJiraIssues map[string]*jira.Issue
	}{
		{
			name:        "empty jira, new alert group",
			inputConfig: testReceiverConfig1(),
			initJira:    func(t *testing.T) *fakeJira { return newTestFakeJira() },
			inputAlert: &alertmanager.Data{
				Alerts: alertmanager.Alerts{
					{Status: alertmanager.AlertFiring},
					{Status: "not firing"},
					{Status: alertmanager.AlertFiring},
				},
				Status:      alertmanager.AlertFiring,
				GroupLabels: alertmanager.KV{"a": "b", "c": "d"},
			},
			expectedJiraIssues: map[string]*jira.Issue{
				"1": {
					ID:  "1",
					Key: "1",
					Fields: &jira.IssueFields{
						Project: jira.Project{Key: testReceiverConfig1().Project},
						Labels:  []string{"JIRALERT{819ba5ecba4ea5946a8d17d285cb23f3bb6862e08bb602ab08fd231cd8e1a83a1d095b0208a661787e9035f0541817634df5a994d1b5d4200d6c68a7663c97f5}"},
						Status: &jira.Status{
							StatusCategory: jira.StatusCategory{Key: "NotDone"},
						},
						Unknowns: tcontainer.MarshalMap{},
						Summary:  "[FIRING:2] b d ",
					},
				},
			},
		},
		{
			name:        "opened ticket, update summary",
			inputConfig: testReceiverConfig1(),
			initJira: func(t *testing.T) *fakeJira {
				f := newTestFakeJira()
				_, _, err := f.Create(&jira.Issue{
					ID:  "1",
					Key: "1",
					Fields: &jira.IssueFields{
						Project:  jira.Project{Key: testReceiverConfig1().Project},
						Labels:   []string{"JIRALERT{819ba5ecba4ea5946a8d17d285cb23f3bb6862e08bb602ab08fd231cd8e1a83a1d095b0208a661787e9035f0541817634df5a994d1b5d4200d6c68a7663c97f5}"},
						Unknowns: tcontainer.MarshalMap{},
						Summary:  "[FIRING:2] b d ",
					},
				})
				require.NoError(t, err)
				return f
			},
			inputAlert: &alertmanager.Data{
				Alerts: alertmanager.Alerts{
					{Status: "not firing"},
					{Status: alertmanager.AlertFiring}, // Only one firing now.
				},
				Status:      alertmanager.AlertFiring,
				GroupLabels: alertmanager.KV{"a": "b", "c": "d"},
			},
			expectedJiraIssues: map[string]*jira.Issue{
				"1": {
					ID:  "1",
					Key: "1",
					Fields: &jira.IssueFields{
						Project: jira.Project{Key: testReceiverConfig1().Project},
						Labels:  []string{"JIRALERT{819ba5ecba4ea5946a8d17d285cb23f3bb6862e08bb602ab08fd231cd8e1a83a1d095b0208a661787e9035f0541817634df5a994d1b5d4200d6c68a7663c97f5}"},
						Status: &jira.Status{
							StatusCategory: jira.StatusCategory{Key: "NotDone"},
						},
						Unknowns: tcontainer.MarshalMap{},
						Summary:  "[FIRING:1] b d ", // Title changed.
					},
				},
			},
		},
		{
			name:        "opened ticket, update summary and description",
			inputConfig: testReceiverConfig2(),
			initJira: func(t *testing.T) *fakeJira {
				f := newTestFakeJira()
				_, _, err := f.Create(&jira.Issue{
					ID:  "1",
					Key: "1",
					Fields: &jira.IssueFields{
						Project:     jira.Project{Key: testReceiverConfig2().Project},
						Labels:      []string{"JIRALERT{819ba5ecba4ea5946a8d17d285cb23f3bb6862e08bb602ab08fd231cd8e1a83a1d095b0208a661787e9035f0541817634df5a994d1b5d4200d6c68a7663c97f5}"},
						Unknowns:    tcontainer.MarshalMap{},
						Summary:     "[FIRING:2] b d ",
						Description: "2",
					},
				})
				require.NoError(t, err)
				return f
			},
			inputAlert: &alertmanager.Data{
				Alerts: alertmanager.Alerts{
					{Status: "not firing"},
					{Status: alertmanager.AlertFiring}, // Only one firing now.
				},
				Status:      alertmanager.AlertFiring,
				GroupLabels: alertmanager.KV{"a": "b", "c": "d"},
			},
			expectedJiraIssues: map[string]*jira.Issue{
				"1": {
					ID:  "1",
					Key: "1",
					Fields: &jira.IssueFields{
						Project: jira.Project{Key: testReceiverConfig2().Project},
						Labels:  []string{"JIRALERT{819ba5ecba4ea5946a8d17d285cb23f3bb6862e08bb602ab08fd231cd8e1a83a1d095b0208a661787e9035f0541817634df5a994d1b5d4200d6c68a7663c97f5}"},
						Status: &jira.Status{
							StatusCategory: jira.StatusCategory{Key: "NotDone"},
						},
						Unknowns:    tcontainer.MarshalMap{},
						Summary:     "[FIRING:1] b d ", // Title changed.
						Description: "1",
					},
				},
			},
		},
		{
			name:        "closed ticket, reopen and update summary",
			inputConfig: testReceiverConfig1(),
			initJira: func(t *testing.T) *fakeJira {
				f := newTestFakeJira()
				_, _, err := f.Create(&jira.Issue{
					ID:  "1",
					Key: "1",
					Fields: &jira.IssueFields{
						Project:  jira.Project{Key: testReceiverConfig1().Project},
						Labels:   []string{"JIRALERT{819ba5ecba4ea5946a8d17d285cb23f3bb6862e08bb602ab08fd231cd8e1a83a1d095b0208a661787e9035f0541817634df5a994d1b5d4200d6c68a7663c97f5}"},
						Unknowns: tcontainer.MarshalMap{},
						Summary:  "[FIRING:2] b d ",
						Resolution: &jira.Resolution{
							Name: "done",
						},
					},
				})
				// Close it.
				f.issuesByKey["1"].Fields.Status.StatusCategory.Key = "done"
				// Resolution time that fits into 1h reopen duration.
				f.issuesByKey["1"].Fields.Resolutiondate = jira.Time(testNowTime.Add(-30 * time.Minute))
				f.transitionsByID["tr1"] = jira.Transition{ID: "tr1", Name: testReceiverConfig1().ReopenState}

				require.NoError(t, err)
				return f
			},
			inputAlert: &alertmanager.Data{
				Alerts: alertmanager.Alerts{
					{Status: "not firing"},
					{Status: alertmanager.AlertFiring}, // Only one firing now.
				},
				Status:      alertmanager.AlertFiring,
				GroupLabels: alertmanager.KV{"a": "b", "c": "d"},
			},
			expectedJiraIssues: map[string]*jira.Issue{
				"1": {
					ID:  "1",
					Key: "1",
					Fields: &jira.IssueFields{
						Project: jira.Project{Key: testReceiverConfig1().Project},
						Labels:  []string{"JIRALERT{819ba5ecba4ea5946a8d17d285cb23f3bb6862e08bb602ab08fd231cd8e1a83a1d095b0208a661787e9035f0541817634df5a994d1b5d4200d6c68a7663c97f5}"},
						Status: &jira.Status{
							StatusCategory: jira.StatusCategory{Key: testReceiverConfig1().ReopenState}, // Status reopened
						},
						Unknowns: tcontainer.MarshalMap{},
						Summary:  "[FIRING:1] b d ", // Title changed.
						Resolution: &jira.Resolution{
							Name: "done",
						},
						Resolutiondate: jira.Time(testNowTime.Add(-30 * time.Minute)),
					},
				},
			},
		},
		{
			name:        "closed won't fix ticket, update summary",
			inputConfig: testReceiverConfig1(),
			initJira: func(t *testing.T) *fakeJira {
				f := newTestFakeJira()
				_, _, err := f.Create(&jira.Issue{
					ID:  "1",
					Key: "1",
					Fields: &jira.IssueFields{
						Project:  jira.Project{Key: testReceiverConfig1().Project},
						Labels:   []string{"JIRALERT{819ba5ecba4ea5946a8d17d285cb23f3bb6862e08bb602ab08fd231cd8e1a83a1d095b0208a661787e9035f0541817634df5a994d1b5d4200d6c68a7663c97f5}"},
						Unknowns: tcontainer.MarshalMap{},
						Summary:  "[FIRING:2] b d ",
						Resolution: &jira.Resolution{
							Name: testReceiverConfig1().WontFixResolution,
						},
					},
				})
				// Close it.
				f.issuesByKey["1"].Fields.Status.StatusCategory.Key = "done"
				// Resolution time that fits into 1h reopen duration.
				f.issuesByKey["1"].Fields.Resolutiondate = jira.Time(testNowTime.Add(-30 * time.Minute))
				f.transitionsByID["tr1"] = jira.Transition{ID: "tr1", Name: testReceiverConfig1().ReopenState}

				require.NoError(t, err)
				return f
			},
			inputAlert: &alertmanager.Data{
				Alerts: alertmanager.Alerts{
					{Status: "not firing"},
					{Status: alertmanager.AlertFiring}, // Only one firing now.
				},
				Status:      alertmanager.AlertFiring,
				GroupLabels: alertmanager.KV{"a": "b", "c": "d"},
			},
			expectedJiraIssues: map[string]*jira.Issue{
				"1": {
					ID:  "1",
					Key: "1",
					Fields: &jira.IssueFields{
						Project: jira.Project{Key: testReceiverConfig1().Project},
						Labels:  []string{"JIRALERT{819ba5ecba4ea5946a8d17d285cb23f3bb6862e08bb602ab08fd231cd8e1a83a1d095b0208a661787e9035f0541817634df5a994d1b5d4200d6c68a7663c97f5}"},
						Status: &jira.Status{
							StatusCategory: jira.StatusCategory{Key: "done"},
						},
						Unknowns: tcontainer.MarshalMap{},
						Summary:  "[FIRING:1] b d ", // Title changed.
						Resolution: &jira.Resolution{
							Name: testReceiverConfig1().WontFixResolution,
						},
						Resolutiondate: jira.Time(testNowTime.Add(-30 * time.Minute)),
					},
				},
			},
		},
		{
			name:        "closed ticket, reopen time exceeded, create and update summary",
			inputConfig: testReceiverConfig1(),
			initJira: func(t *testing.T) *fakeJira {
				f := newTestFakeJira()
				_, _, err := f.Create(&jira.Issue{
					ID:  "1",
					Key: "1",
					Fields: &jira.IssueFields{
						Project:  jira.Project{Key: testReceiverConfig1().Project},
						Labels:   []string{"JIRALERT{819ba5ecba4ea5946a8d17d285cb23f3bb6862e08bb602ab08fd231cd8e1a83a1d095b0208a661787e9035f0541817634df5a994d1b5d4200d6c68a7663c97f5}"},
						Unknowns: tcontainer.MarshalMap{},
						Summary:  "[FIRING:2] b d ",
						Resolution: &jira.Resolution{
							Name: "done",
						},
					},
				})
				// Close it.
				f.issuesByKey["1"].Fields.Status.StatusCategory.Key = "done"
				// Resolution time that does NOT fit into 1h reopen duration.
				f.issuesByKey["1"].Fields.Resolutiondate = jira.Time(testNowTime.Add(-2 * time.Hour))
				f.transitionsByID["tr1"] = jira.Transition{ID: "tr1", Name: testReceiverConfig1().ReopenState}

				require.NoError(t, err)
				return f
			},
			inputAlert: &alertmanager.Data{
				Alerts: alertmanager.Alerts{
					{Status: "not firing"},
					{Status: alertmanager.AlertFiring}, // Only one firing now.
				},
				Status:      alertmanager.AlertFiring,
				GroupLabels: alertmanager.KV{"a": "b", "c": "d"},
			},
			expectedJiraIssues: map[string]*jira.Issue{
				"1": {
					ID:  "1",
					Key: "1",
					Fields: &jira.IssueFields{
						Project: jira.Project{Key: testReceiverConfig1().Project},
						Labels:  []string{"JIRALERT{819ba5ecba4ea5946a8d17d285cb23f3bb6862e08bb602ab08fd231cd8e1a83a1d095b0208a661787e9035f0541817634df5a994d1b5d4200d6c68a7663c97f5}"},
						Status: &jira.Status{
							StatusCategory: jira.StatusCategory{Key: "done"},
						},
						Unknowns: tcontainer.MarshalMap{},
						// Title still obsolete. Current implementation only updates the most
						// "fresh" issue.
						Summary: "[FIRING:2] b d ",
						Resolution: &jira.Resolution{
							Name: "done",
						},
						Resolutiondate: jira.Time(testNowTime.Add(-2 * time.Hour)),
					},
				},
				"2": {
					ID:  "2",
					Key: "2",
					Fields: &jira.IssueFields{
						Project: jira.Project{Key: testReceiverConfig1().Project},
						Labels:  []string{"JIRALERT{819ba5ecba4ea5946a8d17d285cb23f3bb6862e08bb602ab08fd231cd8e1a83a1d095b0208a661787e9035f0541817634df5a994d1b5d4200d6c68a7663c97f5}"},
						Status: &jira.Status{
							StatusCategory: jira.StatusCategory{Key: "NotDone"}, // Created
						},
						Unknowns: tcontainer.MarshalMap{},
						Summary:  "[FIRING:1] b d ", // Title changed.
					},
				},
			},
		},
		{
			name:        "auto resolve alert",
			inputConfig: testReceiverConfigAutoResolve(),
			inputAlert: &alertmanager.Data{
				Alerts: alertmanager.Alerts{
					{Status: "resolved"},
				},
				Status:      alertmanager.AlertResolved,
				GroupLabels: alertmanager.KV{"a": "b", "c": "d"},
			},
			initJira: func(t *testing.T) *fakeJira {
				f := newTestFakeJira()
				_, _, err := f.Create(&jira.Issue{
					ID:  "1",
					Key: "1",
					Fields: &jira.IssueFields{
						Project:     jira.Project{Key: testReceiverConfigAutoResolve().Project},
						Labels:      []string{"JIRALERT{819ba5ecba4ea5946a8d17d285cb23f3bb6862e08bb602ab08fd231cd8e1a83a1d095b0208a661787e9035f0541817634df5a994d1b5d4200d6c68a7663c97f5}"},
						Unknowns:    tcontainer.MarshalMap{},
						Summary:     "[FIRING:2] b d ",
						Description: "1",
					},
				})
				require.NoError(t, err)
				return f
			},
			expectedJiraIssues: map[string]*jira.Issue{
				"1": {
					ID:  "1",
					Key: "1",
					Fields: &jira.IssueFields{
						Project: jira.Project{Key: testReceiverConfigAutoResolve().Project},
						Labels:  []string{"JIRALERT{819ba5ecba4ea5946a8d17d285cb23f3bb6862e08bb602ab08fd231cd8e1a83a1d095b0208a661787e9035f0541817634df5a994d1b5d4200d6c68a7663c97f5}"},
						Status: &jira.Status{
							StatusCategory: jira.StatusCategory{Key: "Done"},
						},
						Unknowns:    tcontainer.MarshalMap{},
						Summary:     "[RESOLVED] b d ", // Title changed.
						Description: "1",
					},
				},
			},
		},
		{
			name:        "empty jira, new alert group with StaticLabels",
			inputConfig: testReceiverConfigWithStaticLabels(),
			initJira:    func(t *testing.T) *fakeJira { return newTestFakeJira() },
			inputAlert: &alertmanager.Data{
				Alerts: alertmanager.Alerts{
					{Status: alertmanager.AlertFiring},
					{Status: "not firing"},
					{Status: alertmanager.AlertFiring},
				},
				Status:      alertmanager.AlertFiring,
				GroupLabels: alertmanager.KV{"a": "b", "c": "d"},
			},
			expectedJiraIssues: map[string]*jira.Issue{
				"1": {
					ID:  "1",
					Key: "1",
					Fields: &jira.IssueFields{
						Project: jira.Project{Key: testReceiverConfig1().Project},
						Labels:  []string{"somelabel", "JIRALERT{819ba5ecba4ea5946a8d17d285cb23f3bb6862e08bb602ab08fd231cd8e1a83a1d095b0208a661787e9035f0541817634df5a994d1b5d4200d6c68a7663c97f5}"},
						Status: &jira.Status{
							StatusCategory: jira.StatusCategory{Key: "NotDone"},
						},
						Unknowns: tcontainer.MarshalMap{},
						Summary:  "[FIRING:2] b d ",
					},
				},
			},
		},
	} {
		if ok := t.Run(tcase.name, func(t *testing.T) {
			fakeJira := tcase.initJira(t)

			receiver := NewReceiver(
				log.NewLogfmtLogger(os.Stderr),
				tcase.inputConfig,
				template.SimpleTemplate(),
				fakeJira,
			)

			receiver.timeNow = func() time.Time {
				return testNowTime
			}

			_, err := receiver.Notify(tcase.inputAlert, true, true, true, true, 32768)
			require.NoError(t, err)
			require.Equal(t, tcase.expectedJiraIssues, fakeJira.issuesByKey)
		}); !ok {
			return
		}
	}
}
