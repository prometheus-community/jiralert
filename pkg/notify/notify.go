package notify

import (
	"bytes"
	"fmt"
	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"io/ioutil"
	"reflect"
	"strings"
	"time"

	"github.com/free/jiralert/pkg/config"
	"github.com/free/jiralert/pkg/template"

	"github.com/andygrunwald/go-jira"
	"github.com/free/jiralert/pkg/alertmanager"
	"github.com/trivago/tgo/tcontainer"
)

// Receiver wraps a JIRA client corresponding to a specific Alertmanager receiver, with its configuration and templates.
type Receiver struct {
	conf   *config.ReceiverConfig
	tmpl   *template.Template
	client *jira.Client
}

// NewReceiver creates a Receiver using the provided configuration and template.
func NewReceiver(c *config.ReceiverConfig, t *template.Template) (*Receiver, error) {
	tp := jira.BasicAuthTransport{
		Username: c.User,
		Password: string(c.Password),
	}
	client, err := jira.NewClient(tp.Client(), c.APIURL)
	if err != nil {
		return nil, err
	}

	return &Receiver{conf: c, tmpl: t, client: client}, nil
}

// Notify implements the Notifier interface.
func (r *Receiver) Notify(data *alertmanager.Data, logger log.Logger) (bool, error) {
	project := r.tmpl.Execute(r.conf.Project, data, logger)
	if err := r.tmpl.Err(); err != nil {
		return false, err
	}
	// Looks like an ALERT metric name, with spaces removed.
	issueLabel := toIssueLabel(data.GroupLabels)

	issue, retry, err := r.search(project, issueLabel, logger)
	if err != nil {
		return retry, err
	}

	if issue != nil {
		// The set of JIRA status categories is fixed, this is a safe check to make.
		if issue.Fields.Status.StatusCategory.Key != "done" {
			// Issue is in a "to do" or "in progress" state, all done here.
			level.Debug(logger).Log("msg", "nothing to do as issue is unresolved", "issue", issue.Key, "label", issueLabel)
			return false, nil
		}
		if r.conf.WontFixResolution != "" && issue.Fields.Resolution != nil &&
			issue.Fields.Resolution.Name == r.conf.WontFixResolution {
			// Issue is resolved as "Won't Fix" or equivalent, log a message just in case.
			level.Info(logger).Log("msg", "issue is resolved, not reopening", "issue", issue.Key, "label", issueLabel, "resolution", issue.Fields.Resolution.Name)
			return false, nil
		}

		resolutionTime := time.Time(issue.Fields.Resolutiondate)
		if resolutionTime.Add(time.Duration(*r.conf.ReopenDuration)).After(time.Now()) {
			level.Info(logger).Log("msg", "issue was resolved after reopen duration, reopening", "issue", issue.Key, "label", issueLabel, "reopenDuration", *r.conf.ReopenDuration, "resolutionTime", resolutionTime)
			return r.reopen(issue.Key, logger)
		}
	}

	level.Info(logger).Log("msg", "no issue, matching the label, found, opening a new one", "label", issueLabel)
	issue = &jira.Issue{
		Fields: &jira.IssueFields{
			Project:     jira.Project{Key: project},
			Type:        jira.IssueType{Name: r.tmpl.Execute(r.conf.IssueType, data, logger)},
			Description: r.tmpl.Execute(r.conf.Description, data, logger),
			Summary:     r.tmpl.Execute(r.conf.Summary, data, logger),
			Labels: []string{
				issueLabel,
			},
			Unknowns: tcontainer.NewMarshalMap(),
		},
	}
	if r.conf.Priority != "" {
		issue.Fields.Priority = &jira.Priority{Name: r.tmpl.Execute(r.conf.Priority, data, logger)}
	}

	// Add Components
	if len(r.conf.Components) > 0 {
		issue.Fields.Components = make([]*jira.Component, 0, len(r.conf.Components))
		for _, component := range r.conf.Components {
			issue.Fields.Components = append(issue.Fields.Components, &jira.Component{Name: r.tmpl.Execute(component, data, logger)})
		}
	}

	// Add Labels
	if r.conf.AddGroupLabels {
		for k, v := range data.GroupLabels {
			issue.Fields.Labels = append(issue.Fields.Labels, fmt.Sprintf("%s=%q", k, v))
		}
	}

	for key, value := range r.conf.Fields {
		issue.Fields.Unknowns[key] = deepCopyWithTemplate(value, r.tmpl, data, logger)
	}

	if err := r.tmpl.Err(); err != nil {
		return false, err
	}
	retry, err = r.create(issue, logger)
	if err == nil {
		level.Info(logger).Log("msg", "issue successfully created", "issue", issue.Key, "issueID", issue.ID)
	}
	return retry, err
}

// deepCopyWithTemplate returns a deep copy of a map/slice/array/string/int/bool or combination thereof, executing the
// provided template (with the provided data) on all string keys or values. All maps are connverted to
// map[string]interface{}, with all non-string keys discarded.
func deepCopyWithTemplate(value interface{}, tmpl *template.Template, data interface{}, logger log.Logger) interface{} {
	if value == nil {
		return value
	}

	valueMeta := reflect.ValueOf(value)
	switch valueMeta.Kind() {

	case reflect.String:
		return tmpl.Execute(value.(string), data, logger)

	case reflect.Array, reflect.Slice:
		arrayLen := valueMeta.Len()
		converted := make([]interface{}, arrayLen)
		for i := 0; i < arrayLen; i++ {
			converted[i] = deepCopyWithTemplate(valueMeta.Index(i).Interface(), tmpl, data, logger)
		}
		return converted

	case reflect.Map:
		keys := valueMeta.MapKeys()
		converted := make(map[string]interface{}, len(keys))

		for _, keyMeta := range keys {
			strKey, isString := keyMeta.Interface().(string)
			if !isString {
				continue
			}
			strKey = tmpl.Execute(strKey, data, logger)
			converted[strKey] = deepCopyWithTemplate(valueMeta.MapIndex(keyMeta).Interface(), tmpl, data, logger)
		}
		return converted

	default:
		return value
	}
}

// toIssueLabel returns the group labels in the form of an ALERT metric name, with all spaces removed.
func toIssueLabel(groupLabels alertmanager.KV) string {
	buf := bytes.NewBufferString("ALERT{")
	for _, p := range groupLabels.SortedPairs() {
		buf.WriteString(p.Name)
		buf.WriteString(fmt.Sprintf("=%q,", p.Value))
	}
	buf.Truncate(buf.Len() - 1)
	buf.WriteString("}")
	return strings.Replace(buf.String(), " ", "", -1)
}

func (r *Receiver) search(project, issueLabel string, logger log.Logger) (*jira.Issue, bool, error) {
	query := fmt.Sprintf("project=\"%s\" and labels=%q order by resolutiondate desc", project, issueLabel)
	options := &jira.SearchOptions{
		Fields:     []string{"summary", "status", "resolution", "resolutiondate"},
		MaxResults: 2,
	}
	level.Debug(logger).Log("msg", "searching for existing issue", "query", query, "options", options)
	issues, resp, err := r.client.Issue.Search(query, options)
	if err != nil {
		retry, err := handleJiraError("Issue.Search", resp, err, logger)
		return nil, retry, err
	}
	if len(issues) > 0 {
		if len(issues) > 1 {
			// Swallow it, but log a message.
			level.Warn(logger).Log("msg", "more than one issue matched, updating only the last one", "query", query, "issues", issues)
		}

		level.Debug(logger).Log("msg", "found existing issue matching the query", "issue", issues[0], "query", query)
		return &issues[0], false, nil
	}
	level.Debug(logger).Log("msg", "no existing issues matching query found", "query", query)
	return nil, false, nil
}

func (r *Receiver) reopen(issueKey string, logger log.Logger) (bool, error) {
	transitions, resp, err := r.client.Issue.GetTransitions(issueKey)
	if err != nil {
		return handleJiraError("Issue.GetTransitions", resp, err, logger)
	}
	for _, t := range transitions {
		if t.Name == r.conf.ReopenState {
			level.Debug(logger).Log("msg", "reopening issue", "issue", issueKey, "transitionID", t.ID)
			resp, err = r.client.Issue.DoTransition(issueKey, t.ID)
			if err != nil {
				return handleJiraError("Issue.DoTransition", resp, err, logger)
			}

			level.Debug(logger).Log("msg", "issue successfully reopened")
			return false, nil
		}
	}
	return false, fmt.Errorf("JIRA state %q does not exist or no transition possible for %s", r.conf.ReopenState, issueKey)
}

func (r *Receiver) create(issue *jira.Issue, logger log.Logger) (bool, error) {
	level.Debug(logger).Log("msg", "creating new issue", "issue", *issue)
	newIssue, resp, err := r.client.Issue.Create(issue)
	if err != nil {
		return handleJiraError("Issue.Create", resp, err, logger)
	}
	*issue = *newIssue

	level.Debug(logger).Log("msg", "issue successfully created", "issue", issue.Key, "issueID", issue.ID)
	return false, nil
}

func handleJiraError(api string, resp *jira.Response, err error, logger log.Logger) (bool, error) {
	if resp == nil || resp.Request == nil {
		level.Debug(logger).Log("msg", "handleJiraError", "api", api, "err", err)
	} else {
		level.Debug(logger).Log("msg", "handleJiraError", "api", api, "err", err, "url", resp.Request.URL)
	}

	if resp != nil && resp.StatusCode/100 != 2 {
		retry := resp.StatusCode == 500 || resp.StatusCode == 503
		body, _ := ioutil.ReadAll(resp.Body)
		// go-jira error message is not particularly helpful, replace it
		return retry, fmt.Errorf("JIRA request %s returned status %s, body %q", resp.Request.URL, resp.Status, string(body))
	}
	return false, fmt.Errorf("JIRA request %s failed: %s", api, err)
}
