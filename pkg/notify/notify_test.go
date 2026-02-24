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
	"sort"
	"testing"
	"time"

	"github.com/andygrunwald/go-jira"

	"github.com/pkg/errors"
	"github.com/prometheus-community/jiralert/pkg/alertmanager"
	"github.com/stretchr/testify/require"
)

func TestToGroupTicketLabel(t *testing.T) {
	require.Equal(t, `JIRALERT{9897cb21a3d1ba47d2aab501ce9bc60b74bf65e26658f8e34a7fc81705e6b6eadfe6ad8edfe7c68142b3fe10f2c89127bd85e5f3687fe6b9ff1eff4b3f71dd49}`, toGroupTicketLabel(alertmanager.KV{"a": "B", "C": "d"}, true))
	require.Equal(t, `ALERT{C="d",a="B"}`, toGroupTicketLabel(alertmanager.KV{"a": "B", "C": "d"}, false))
}

type fakeJira struct {
	// Key = ID for simplification.
	issuesByKey     map[string]*jira.Issue
	keysByQuery     map[string][]string
	transitionsByID map[string]jira.Transition
}

func newTestFakeJira() *fakeJira {
	return &fakeJira{
		issuesByKey:     map[string]*jira.Issue{},
		transitionsByID: map[string]jira.Transition{"1234": {ID: "1234", Name: "Done"}},
		keysByQuery:     map[string][]string{},
	}
}

// SearchV2JQL matches the new interface signature.
func (f *fakeJira) SearchV2JQL(jql string, options *jira.SearchOptionsV2) ([]jira.Issue, *jira.Response, error) {
	var issues []jira.Issue
	for _, key := range f.keysByQuery[jql] {
		issue := jira.Issue{Key: key, Fields: &jira.IssueFields{}}
		for _, field := range options.Fields {
			switch field {
			case "summary":
				issue.Fields.Summary = f.issuesByKey[key].Fields.Summary
			case "description":
				issue.Fields.Description = f.issuesByKey[key].Fields.Description
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
			case "priority":
				if f.issuesByKey[key].Fields.Priority != nil {
					issue.Fields.Priority = &jira.Priority{
						Name: f.issuesByKey[key].Fields.Priority.Name,
					}
				}
			}
		}
		issues = append(issues, issue)
	}

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
		issue.Fields.Labels[len(issue.Fields.Labels)-1],
	)
	f.keysByQuery[query] = append(f.keysByQuery[query], issue.Key)

	return issue, nil, nil
}

// UpdateWithOptions matches the new interface using interface{} for options.
func (f *fakeJira) UpdateWithOptions(old *jira.Issue, _ *jira.UpdateQueryOptions) (*jira.Issue, *jira.Response, error) {
	issue, ok := f.issuesByKey[old.Key]
	if !ok {
		return nil, nil, errors.Errorf("no such issue %s", old.Key)
	}

	if old.Fields.Summary != "" {
		issue.Fields.Summary = old.Fields.Summary
	}

	if old.Fields.Priority != nil {
		issue.Fields.Priority = old.Fields.Priority
	}

	if old.Fields.Description != "" {
		issue.Fields.Description = old.Fields.Description
	}

	f.issuesByKey[issue.Key] = issue
	return issue, nil, nil
}

func (f *fakeJira) AddComment(issueID string, comment *jira.Comment) (*jira.Comment, *jira.Response, error) {
	if f.issuesByKey[issueID].Fields.Comments == nil {
		f.issuesByKey[issueID].Fields.Comments = &jira.Comments{}
	}
	f.issuesByKey[issueID].Fields.Comments.Comments = append(f.issuesByKey[issueID].Fields.Comments.Comments, comment)
	return comment, nil, nil
}

func (f *fakeJira) DoTransition(ticketID, transitionID string) (*jira.Response, error) {
	issue, ok := f.issuesByKey[ticketID]
	if !ok {
		return nil, errors.Errorf("no such issue %s", ticketID)
	}

	tr, ok := f.transitionsByID[transitionID]
	if !ok {
		return nil, errors.Errorf("no such transition %s", transitionID)
	}

	issue.Fields.Status.StatusCategory.Key = tr.Name
	f.issuesByKey[issue.Key] = issue

	return nil, nil
}

func TestMocks(t *testing.T) {
	f := newTestFakeJira()

	// 1. Initialize the mock state properly
	// We need an issue with a Key "1" and initialized Fields
	f.issuesByKey["1"] = &jira.Issue{
		Key: "1",
		Fields: &jira.IssueFields{
			Summary: "Original Summary",
			Status: &jira.Status{
				StatusCategory: jira.StatusCategory{Key: "done"},
			},
		},
	}
	f.keysByQuery["project='PROJ'"] = []string{"1"}

	// 2. Run the calls
	_, _, _ = f.SearchV2JQL("project='PROJ'", &jira.SearchOptionsV2{MaxResults: 1})
	_, _, _ = f.GetTransitions("1")

	dummyIssue := &jira.Issue{
		Fields: &jira.IssueFields{
			Project: jira.Project{Key: "PROJ"},
			Labels:  []string{"test-label"},
		},
	}
	_, _, _ = f.Create(dummyIssue)

	existingIssue := f.issuesByKey["1"]
	_, _, _ = f.UpdateWithOptions(existingIssue, nil)

	_, _, _ = f.AddComment("1", &jira.Comment{Body: "lint fix"})
	_, _ = f.DoTransition("1", "1234")

	require.NotNil(t, f)
}
