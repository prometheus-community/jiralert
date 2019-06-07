package notify

import (
	"fmt"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/trivago/tgo/tcontainer"

	"github.com/andygrunwald/go-jira"
	"github.com/free/jiralert/pkg/alertmanager"
	"github.com/free/jiralert/pkg/config"
	"github.com/free/jiralert/pkg/template"
	"github.com/go-kit/kit/log"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestToGroupTicketLabel(t *testing.T) {
	require.Equal(t, `ALERT{C="d",a="B"}`, toGroupTicketLabel(alertmanager.KV{"a": "B", "C": "d"}))
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
		transitionsByID: map[string]jira.Transition{},
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
		"project=\"%s\" and labels=%q order by resolutiondate desc",
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

	issue.Fields.Summary = old.Fields.Summary
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
						Labels:  []string{"ALERT{a=\"b\",c=\"d\"}"},
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
						Labels:   []string{"ALERT{a=\"b\",c=\"d\"}"},
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
						Labels:  []string{"ALERT{a=\"b\",c=\"d\"}"},
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
			name:        "closed ticket, reopen and update summary",
			inputConfig: testReceiverConfig1(),
			initJira: func(t *testing.T) *fakeJira {
				f := newTestFakeJira()
				_, _, err := f.Create(&jira.Issue{
					ID:  "1",
					Key: "1",
					Fields: &jira.IssueFields{
						Project:  jira.Project{Key: testReceiverConfig1().Project},
						Labels:   []string{"ALERT{a=\"b\",c=\"d\"}"},
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
						Labels:  []string{"ALERT{a=\"b\",c=\"d\"}"},
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
						Labels:   []string{"ALERT{a=\"b\",c=\"d\"}"},
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
						Labels:  []string{"ALERT{a=\"b\",c=\"d\"}"},
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
						Labels:   []string{"ALERT{a=\"b\",c=\"d\"}"},
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
						Labels:  []string{"ALERT{a=\"b\",c=\"d\"}"},
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
						Labels:  []string{"ALERT{a=\"b\",c=\"d\"}"},
						Status: &jira.Status{
							StatusCategory: jira.StatusCategory{Key: "NotDone"}, // Created
						},
						Unknowns: tcontainer.MarshalMap{},
						Summary:  "[FIRING:1] b d ", // Title changed.
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

			_, err := receiver.Notify(tcase.inputAlert)
			require.NoError(t, err)
			require.Equal(t, tcase.expectedJiraIssues, fakeJira.issuesByKey)
		}); !ok {
			return
		}
	}
}
