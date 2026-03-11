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
	"bytes"
	"crypto/sha512"
	"fmt"
	"io"
	"reflect"
	"strings"
	"time"

	"github.com/andygrunwald/go-jira"
	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/pkg/errors"
	"github.com/prometheus-community/jiralert/pkg/alertmanager"
	"github.com/prometheus-community/jiralert/pkg/config"
	"github.com/prometheus-community/jiralert/pkg/template"
	"github.com/trivago/tgo/tcontainer"
)

// TODO(bwplotka): Consider renaming this package to ticketer.

type jiraIssueService interface {
	SearchV2JQL(jql string, options *jira.SearchOptionsV2) ([]jira.Issue, *jira.Response, error)
	GetTransitions(id string) ([]jira.Transition, *jira.Response, error)

	Create(issue *jira.Issue) (*jira.Issue, *jira.Response, error)
	UpdateWithOptions(issue *jira.Issue, opts *jira.UpdateQueryOptions) (*jira.Issue, *jira.Response, error)
	AddComment(issueID string, comment *jira.Comment) (*jira.Comment, *jira.Response, error)
	DoTransition(ticketID, transitionID string) (*jira.Response, error)
}

// Receiver wraps a specific Alertmanager receiver with its configuration and templates, creating/updating/reopening Jira issues based on Alertmanager notifications.
type Receiver struct {
	logger log.Logger
	client jiraIssueService
	// TODO(bwplotka): Consider splitting receiver config with ticket service details.
	conf *config.ReceiverConfig
	tmpl *template.Template

	timeNow func() time.Time
}

// NewReceiver creates a Receiver using the provided configuration, template and jiraIssueService.
func NewReceiver(logger log.Logger, c *config.ReceiverConfig, t *template.Template, client jiraIssueService) *Receiver {
	return &Receiver{logger: logger, conf: c, tmpl: t, client: client, timeNow: time.Now}
}

// Notify manages JIRA issues based on alertmanager webhook notify message.
func (r *Receiver) Notify(data *alertmanager.Data, hashJiraLabel bool, updateSummary bool, updateDescription bool, reopenTickets bool, maxDescriptionLength int, updatePriority bool) (bool, error) {
	project, err := r.tmpl.Execute(r.conf.Project, data)
	if err != nil {
		return false, errors.Wrap(err, "generate project from template")
	}

	issueGroupLabel := toGroupTicketLabel(data.GroupLabels, hashJiraLabel)

	issue, retry, err := r.findIssueToReuse(project, issueGroupLabel)
	if err != nil {
		return retry, err
	}

	// We want up to date title no matter what.
	// This allows reflecting current group state if desired by user e.g {{ len $.Alerts.Firing() }}
	issueSummary, err := r.tmpl.Execute(r.conf.Summary, data)
	if err != nil {
		return false, errors.Wrap(err, "generate summary from template")
	}

	issuePriority, err := r.tmpl.Execute(r.conf.Priority, data)
	if err != nil {
		return false, errors.Wrap(err, "generate priority from template")
	}

	issueDesc, err := r.tmpl.Execute(r.conf.Description, data)
	if err != nil {
		return false, errors.Wrap(err, "render issue description")
	}

	if len(issueDesc) > maxDescriptionLength {
		level.Warn(r.logger).Log("msg", "truncating description", "original", len(issueDesc), "limit", maxDescriptionLength)
		issueDesc = issueDesc[:maxDescriptionLength]
	}

	if issue != nil {

		// Update summary if needed.
		if updateSummary {
			if issue.Fields.Summary != issueSummary {
				level.Debug(r.logger).Log("updateSummaryDisabled executing")
				retry, err := r.updateSummary(issue.Key, issueSummary)
				if err != nil {
					return retry, err
				}
			}
		}

		if r.conf.UpdateInComment != nil && *r.conf.UpdateInComment {
			numComments := 0
			if issue.Fields.Comments != nil {
				numComments = len(issue.Fields.Comments.Comments)
			}
			if numComments > 0 && issue.Fields.Comments.Comments[(numComments-1)].Body == issueDesc {
				// if the new comment is identical to the most recent comment,
				// this is probably due to the prometheus repeat_interval and should not be added.
				level.Debug(r.logger).Log("msg", "not adding new comment identical to last", "key", issue.Key)
			} else if numComments == 0 && issue.Fields.Description == issueDesc {
				// if the first comment is identical to the description,
				// this is probably due to the prometheus repeat_interval and should not be added.
				level.Debug(r.logger).Log("msg", "not adding comment identical to description", "key", issue.Key)
			} else {
				retry, err := r.addComment(issue.Key, issueDesc)
				if err != nil {
					return retry, err
				}
			}
		}

		// update description if enabled. This has to be done after comment adding logic which needs to handle redundant commentary vs description case.
		if updateDescription {
			if issue.Fields.Description != issueDesc {
				retry, err := r.updateDescription(issue.Key, issueDesc)
				if err != nil {
					return retry, err
				}
			}
		}

		if updatePriority && issue.Fields.Priority != nil {
			if issue.Fields.Priority.Name != issuePriority {
				level.Debug(r.logger).Log("msg", "updating priority", "key", issue.Key, "new_priority", issuePriority)
				retry, err := r.updatePriority(issue.Key, issuePriority)
				if err != nil {
					return retry, err
				}
			}
		}

		if len(data.Alerts.Firing()) == 0 {
			if r.conf.AutoResolve != nil {
				level.Debug(r.logger).Log("msg", "no firing alert; resolving issue", "key", issue.Key, "label", issueGroupLabel)
				retry, err := r.resolveIssue(issue.Key)
				if err != nil {
					return retry, err
				}
				return false, nil
			}

			level.Debug(r.logger).Log("msg", "no firing alert; summary checked, nothing else to do.", "key", issue.Key, "label", issueGroupLabel)
			return false, nil
		}

		// The set of JIRA status categories is fixed, this is a safe check to make.
		if issue.Fields.Status.StatusCategory.Key != "done" {
			level.Debug(r.logger).Log("msg", "issue is unresolved, all is done", "key", issue.Key, "label", issueGroupLabel)
			return false, nil
		}

		if reopenTickets {
			if r.conf.WontFixResolution != "" && issue.Fields.Resolution != nil &&
				issue.Fields.Resolution.Name == r.conf.WontFixResolution {
				level.Info(r.logger).Log("msg", "issue was resolved as won't fix, not reopening", "key", issue.Key, "label", issueGroupLabel, "resolution", issue.Fields.Resolution.Name)
				return false, nil
			}

			level.Info(r.logger).Log("msg", "issue was recently resolved, reopening", "key", issue.Key, "label", issueGroupLabel)
			return r.reopen(issue.Key)
		}

		level.Debug(r.logger).Log("Did not update anything")
		return false, nil
	}

	if len(data.Alerts.Firing()) == 0 {
		level.Debug(r.logger).Log("msg", "no firing alert; nothing to do.", "label", issueGroupLabel)
		return false, nil
	}

	level.Info(r.logger).Log("msg", "no recent matching issue found, creating new issue", "label", issueGroupLabel)

	issueType, err := r.tmpl.Execute(r.conf.IssueType, data)
	if err != nil {
		return false, errors.Wrap(err, "render issue type")
	}

	staticLabels := r.conf.StaticLabels

	issue = &jira.Issue{
		Fields: &jira.IssueFields{
			Project:     jira.Project{Key: project},
			Type:        jira.IssueType{Name: issueType},
			Description: issueDesc,
			Summary:     issueSummary,
			Labels:      append(staticLabels, issueGroupLabel),
			Unknowns:    tcontainer.NewMarshalMap(),
		},
	}
	if r.conf.Priority != "" {
		issuePrio, err := r.tmpl.Execute(r.conf.Priority, data)
		if err != nil {
			return false, errors.Wrap(err, "render issue priority")
		}

		issue.Fields.Priority = &jira.Priority{Name: issuePrio}
	}

	if len(r.conf.Components) > 0 {
		issue.Fields.Components = make([]*jira.Component, 0, len(r.conf.Components))
		for _, component := range r.conf.Components {
			issueComp, err := r.tmpl.Execute(component, data)
			if err != nil {
				return false, errors.Wrap(err, "render issue component")
			}

			issue.Fields.Components = append(issue.Fields.Components, &jira.Component{Name: issueComp})
		}
	}

	if r.conf.AddGroupLabels != nil && *r.conf.AddGroupLabels {
		for k, v := range data.GroupLabels {
			issue.Fields.Labels = append(issue.Fields.Labels, fmt.Sprintf("%s=%.200q", k, v))
		}
	}

	for key, value := range r.conf.Fields {
		issue.Fields.Unknowns[key], err = deepCopyWithTemplate(value, r.tmpl, data)
		if err != nil {
			return false, err
		}
	}

	return r.create(issue)
}

// deepCopyWithTemplate returns a deep copy of a map/slice/array/string/int/bool or combination thereof, executing the
// provided template (with the provided data) on all string keys or values. All maps are connverted to
// map[string]interface{}, with all non-string keys discarded.
func deepCopyWithTemplate(value interface{}, tmpl *template.Template, data interface{}) (interface{}, error) {
	if value == nil {
		return value, nil
	}

	valueMeta := reflect.ValueOf(value)
	switch valueMeta.Kind() {

	case reflect.String:
		return tmpl.Execute(value.(string), data)

	case reflect.Array, reflect.Slice:
		arrayLen := valueMeta.Len()
		converted := make([]interface{}, arrayLen)
		for i := 0; i < arrayLen; i++ {
			var err error
			converted[i], err = deepCopyWithTemplate(valueMeta.Index(i).Interface(), tmpl, data)
			if err != nil {
				return nil, err
			}
		}
		return converted, nil

	case reflect.Map:
		keys := valueMeta.MapKeys()
		converted := make(map[string]interface{}, len(keys))

		for _, keyMeta := range keys {
			var err error
			strKey, isString := keyMeta.Interface().(string)
			if !isString {
				continue
			}
			strKey, err = tmpl.Execute(strKey, data)
			if err != nil {
				return nil, err
			}
			converted[strKey], err = deepCopyWithTemplate(valueMeta.MapIndex(keyMeta).Interface(), tmpl, data)
			if err != nil {
				return nil, err
			}
		}
		return converted, nil
	default:
		return value, nil
	}
}

// toGroupTicketLabel returns the group labels as a single string.
// This is used to reference each ticket groups.
// (old) default behavior: String is the form of an ALERT Prometheus metric name, with all spaces removed.
// new opt-in behavior: String is the form of JIRALERT{sha512hash(groupLabels)}
// hashing ensures that JIRA validation still accepts the output even
// if the combined length of all groupLabel key-value pairs would be
// longer than 255 chars
func toGroupTicketLabel(groupLabels alertmanager.KV, hashJiraLabel bool) string {
	// new opt in behavior
	if hashJiraLabel {
		hash := sha512.New()
		for _, p := range groupLabels.SortedPairs() {
			kvString := fmt.Sprintf("%s=%q,", p.Name, p.Value)
			_, _ = hash.Write([]byte(kvString)) // hash.Write can never return an error
		}
		return fmt.Sprintf("JIRALERT{%x}", hash.Sum(nil))
	}

	// old default behavior
	buf := bytes.NewBufferString("ALERT{")
	for _, p := range groupLabels.SortedPairs() {
		buf.WriteString(p.Name)
		buf.WriteString(fmt.Sprintf("=%q,", p.Value))
	}
	buf.Truncate(buf.Len() - 1)
	buf.WriteString("}")
	return strings.Replace(buf.String(), " ", "", -1)
}

func (r *Receiver) search(projects []string, issueLabel string) (*jira.Issue, bool, error) {
	// Search multiple projects in case issue was moved and further alert firings are desired in existing JIRA.
	projectList := "'" + strings.Join(projects, "', '") + "'"
	query := fmt.Sprintf("project in(%s) and labels=%q order by resolutiondate desc", projectList, issueLabel)
	options := &jira.SearchOptionsV2{
		Fields:     []string{"summary", "priority", "status", "resolution", "resolutiondate", "description", "comment"},
		MaxResults: 2,
	}

	level.Debug(r.logger).Log("msg", "search", "query", query, "options", fmt.Sprintf("%+v", options))
	issues, resp, err := r.client.SearchV2JQL(query, options)
	if err != nil {
		retry, err := handleJiraErrResponse("Issue.Search", resp, err, r.logger)
		return nil, retry, err
	}

	if len(issues) == 0 {
		level.Debug(r.logger).Log("msg", "no results", "query", query)
		return nil, false, nil
	}

	issue := issues[0]
	if len(issues) > 1 {
		level.Warn(r.logger).Log("msg", "more than one issue matched, picking most recently resolved", "query", query, "issues", issues, "picked", issue)
	}

	level.Debug(r.logger).Log("msg", "found", "issue", issue, "query", query)
	return &issue, false, nil
}

func (r *Receiver) findIssueToReuse(project string, issueGroupLabel string) (*jira.Issue, bool, error) {
	projectsToSearch := []string{project}
	// In case issue was moved to a different project, include the other configured projects in search (if any).
	for _, other := range r.conf.OtherProjects {
		if other != project {
			projectsToSearch = append(projectsToSearch, other)
		}
	}

	issue, retry, err := r.search(projectsToSearch, issueGroupLabel)
	if err != nil {
		return nil, retry, err
	}

	if issue == nil {
		return nil, false, nil
	}

	resolutionTime := time.Time(issue.Fields.Resolutiondate)
	if resolutionTime != (time.Time{}) && resolutionTime.Add(time.Duration(*r.conf.ReopenDuration)).Before(r.timeNow()) && *r.conf.ReopenDuration != 0 {
		level.Debug(r.logger).Log("msg", "existing resolved issue is too old to reopen, skipping", "key", issue.Key, "label", issueGroupLabel, "resolution_time", resolutionTime.Format(time.RFC3339), "reopen_duration", *r.conf.ReopenDuration)
		return nil, false, nil
	}

	// Reuse issue.
	return issue, false, nil
}

func (r *Receiver) updateSummary(issueKey string, summary string) (bool, error) {
	level.Debug(r.logger).Log("msg", "updating issue with new summary", "key", issueKey, "summary", summary)

	issueUpdate := &jira.Issue{
		Key: issueKey,
		Fields: &jira.IssueFields{
			Summary: summary,
		},
	}
	issue, resp, err := r.client.UpdateWithOptions(issueUpdate, nil)
	if err != nil {
		return handleJiraErrResponse("Issue.UpdateWithOptions", resp, err, r.logger)
	}
	level.Debug(r.logger).Log("msg", "issue summary updated", "key", issue.Key, "id", issue.ID)
	return false, nil
}

func (r *Receiver) updateDescription(issueKey string, description string) (bool, error) {
	level.Debug(r.logger).Log("msg", "updating issue with new description", "key", issueKey, "description", description)

	issueUpdate := &jira.Issue{
		Key: issueKey,
		Fields: &jira.IssueFields{
			Description: description,
		},
	}
	issue, resp, err := r.client.UpdateWithOptions(issueUpdate, nil)
	if err != nil {
		return handleJiraErrResponse("Issue.UpdateWithOptions", resp, err, r.logger)
	}
	level.Debug(r.logger).Log("msg", "issue summary updated", "key", issue.Key, "id", issue.ID)
	return false, nil
}

func (r *Receiver) addComment(issueKey string, content string) (bool, error) {
	level.Debug(r.logger).Log("msg", "adding comment to existing issue", "key", issueKey, "content", content)

	commentDetails := &jira.Comment{
		Body: content,
	}

	comment, resp, err := r.client.AddComment(issueKey, commentDetails)
	if err != nil {
		return handleJiraErrResponse("Issue.AddComment", resp, err, r.logger)
	}
	level.Debug(r.logger).Log("msg", "added comment to issue", "key", issueKey, "id", comment.ID)
	return false, nil
}

func (r *Receiver) reopen(issueKey string) (bool, error) {
	return r.doTransition(issueKey, r.conf.ReopenState)
}

func (r *Receiver) create(issue *jira.Issue) (bool, error) {
	level.Debug(r.logger).Log("msg", "create", "issue", fmt.Sprintf("%+v", *issue.Fields))
	newIssue, resp, err := r.client.Create(issue)
	if err != nil {
		return handleJiraErrResponse("Issue.Create", resp, err, r.logger)
	}
	*issue = *newIssue

	level.Info(r.logger).Log("msg", "issue created", "key", issue.Key, "id", issue.ID)
	return false, nil
}

func handleJiraErrResponse(api string, resp *jira.Response, err error, logger log.Logger) (bool, error) {
	if resp == nil || resp.Request == nil {
		level.Debug(logger).Log("msg", "handleJiraErrResponse", "api", api, "err", err)
	} else {
		level.Debug(logger).Log("msg", "handleJiraErrResponse", "api", api, "err", err, "url", resp.Request.URL)
	}

	if resp != nil && resp.StatusCode/100 != 2 {
		retry := resp.StatusCode == 500 || resp.StatusCode == 503 || resp.StatusCode == 429
		// Sometimes go-jira consumes the body (e.g. in `Search`) and includes it in the error message;
		// sometimes (e.g. in `Create`) it doesn't. Include both the error and the body, just in case.
		body, _ := io.ReadAll(resp.Body)
		return retry, errors.Errorf("JIRA request %s returned status %s, error %q, body %q", resp.Request.URL, resp.Status, err, body)
	}
	return false, errors.Wrapf(err, "JIRA request %s failed", api)
}

func (r *Receiver) resolveIssue(issueKey string) (bool, error) {
	return r.doTransition(issueKey, r.conf.AutoResolve.State)
}

func (r *Receiver) doTransition(issueKey string, transitionState string) (bool, error) {
	transitions, resp, err := r.client.GetTransitions(issueKey)
	if err != nil {
		return handleJiraErrResponse("Issue.GetTransitions", resp, err, r.logger)
	}

	for _, t := range transitions {
		if t.Name == transitionState {
			level.Debug(r.logger).Log("msg", fmt.Sprintf("transition %s", transitionState), "key", issueKey, "transitionID", t.ID)
			resp, err = r.client.DoTransition(issueKey, t.ID)
			if err != nil {
				return handleJiraErrResponse("Issue.DoTransition", resp, err, r.logger)
			}

			level.Debug(r.logger).Log("msg", transitionState, "key", issueKey)
			return false, nil
		}
	}
	return false, errors.Errorf("JIRA state %q does not exist or no transition possible for %s", transitionState, issueKey)

}

func (r *Receiver) updatePriority(issueKey string, priority string) (bool, error) {
	level.Debug(r.logger).Log("msg", "updating issue with new priority", "key", issueKey, "priority", priority)
	issueUpdate := &jira.Issue{
		Key: issueKey,
		Fields: &jira.IssueFields{
			Priority: &jira.Priority{Name: priority},
		},
	}
	issue, resp, err := r.client.UpdateWithOptions(issueUpdate, nil)
	if err != nil {
		return handleJiraErrResponse("Issue.UpdateWithOptions", resp, err, r.logger)
	}
	level.Debug(r.logger).Log("msg", "issue priority updated", "key", issue.Key, "id", issue.ID)
	return false, nil
}
