package jiralert

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"

	"github.com/andygrunwald/go-jira"
	"github.com/prometheus/alertmanager/template"
	"github.com/prometheus/common/log"
	"github.com/trivago/tgo/tcontainer"
)

type Jira struct {
	conf *JiraConfig
	tmpl *Template
}

func NewJira(c *JiraConfig, t *Template) *Jira {
	return &Jira{conf: c, tmpl: t}
}

// Notify implements the Notifier interface.
func (n *Jira) Notify(data *template.Data) (bool, error) {
	client, err := jira.NewClient(http.DefaultClient, n.conf.APIURL)
	if err != nil {
		return false, err
	}
	client.Authentication.SetBasicAuth(n.conf.User, string(n.conf.Password))

	project := n.tmpl.Execute(n.conf.Project, data)
	// check errors from n.tmpl.Execute()
	if n.tmpl.err != nil {
		return false, n.tmpl.err
	}

	// Looks like an ALERT metric name, with spaces removed.
	issueLabel := toIssueLabel(data.GroupLabels)
	issue, retry, err := n.search(client, project, issueLabel)
	if err != nil {
		return retry, err
	}

	if issue != nil {
		log.Debugf("Found existing issue: %+v", issue)
		// The set of Jira status categories is fixed, this is a safe check to make.
		if issue.Fields.Status.StatusCategory.Key != "done" {
			// Issue is in a "to do" or "in progress" state, all done here.
			log.Debugf("Issue %s for %s is unresolved, nothing to do", issue.Key, issueLabel)
			return false, nil
		}
		if n.conf.WontFixResolution != "" && issue.Fields.Resolution.Name == n.conf.WontFixResolution {
			// Issue is resolved as "Won't Fix" or equivalent, log a warning just in case.
			log.Warnf("Issue %s for %s is resolved as %q, not reopening", issue.Key, issueLabel, issue.Fields.Resolution.Name)
			return false, nil
		}
		log.Debugf("Issue %s for %s was resolved, reopening", issue.Key, issueLabel)
		return n.reopen(client, issue.Key)
	}

	issue = &jira.Issue{
		Fields: &jira.IssueFields{
			Project:     jira.Project{Key: project},
			Type:        jira.IssueType{Name: n.tmpl.Execute(n.conf.IssueType, data)},
			Description: n.tmpl.Execute(n.conf.Description, data),
			Summary:     n.tmpl.Execute(n.conf.Summary, data),
			Labels: []string{
				issueLabel,
			},
			Unknowns: tcontainer.NewMarshalMap(),
		},
	}
	if n.conf.Priority != "" {
		issue.Fields.Priority = &jira.Priority{Name: n.tmpl.Execute(n.conf.Priority, data)}
	}
	for key, value := range n.conf.Fields {
		issue.Fields.Unknowns[key] = n.tmpl.Execute(fmt.Sprint(value), data)
	}
	// check errors from n.tmpl.Execute()
	if n.tmpl.err != nil {
		return false, n.tmpl.err
	}
	return n.create(client, issue)
}

// toIssueLabel returns the group labels in the form of an ALERT metric name, with all spaces removed.
func toIssueLabel(groupLabels template.KV) string {
	buf := bytes.NewBufferString("ALERT{")
	for _, p := range groupLabels.SortedPairs() {
		buf.WriteString(p.Name)
		buf.WriteString(fmt.Sprintf("=%q", p.Value))
	}
	buf.WriteString("}")
	return strings.Replace(buf.String(), " ", "", -1)
}

func (n *Jira) search(client *jira.Client, project, issueLabel string) (*jira.Issue, bool, error) {
	query := fmt.Sprintf("project=%s and labels=%q order by key", project, issueLabel)
	options := &jira.SearchOptions{
		Fields:     []string{"summary", "status", "resolution"},
		MaxResults: 50,
	}
	issues, resp, err := client.Issue.Search(query, options)
	if err != nil {
		retry, err := handleJiraError(resp, err)
		return nil, retry, err
	}
	if len(issues) > 0 {
		if len(issues) > 1 {
			// Swallow it, but log an error.
			log.Errorf("More than one issue matched %s, will only update first: %+v", query, issues)
		}
		return &issues[0], false, nil
	}
	return nil, false, nil
}

func (n *Jira) reopen(client *jira.Client, issueKey string) (bool, error) {
	transitions, resp, err := client.Issue.GetTransitions(issueKey)
	if err != nil {
		return handleJiraError(resp, err)
	}
	for _, t := range transitions {
		if t.Name == n.conf.ReopenState {
			resp, err = client.Issue.DoTransition(issueKey, t.ID)
			if err != nil {
				return handleJiraError(resp, err)
			}
			return false, nil
		}
	}
	return false, fmt.Errorf("Jira state %q does not exist or no transition possible for %s", n.conf.ReopenState, issueKey)
}

func (n *Jira) create(client *jira.Client, issue *jira.Issue) (bool, error) {
	issue, resp, err := client.Issue.Create(issue)
	if err != nil {
		return handleJiraError(resp, err)
	}

	log.Debugf("Created issue %s (ID: %s)", issue.Key, issue.ID)
	return false, nil
}

func handleJiraError(resp *jira.Response, err error) (bool, error) {
	if resp != nil && resp.StatusCode/100 != 2 {
		retry := resp.StatusCode == 500 || resp.StatusCode == 503
		body, _ := ioutil.ReadAll(resp.Body)
		// go-jira error message is not particularly helpful, replace it
		return retry, fmt.Errorf("Jira request %s returned status %d, body %q", resp.Request.URL.String(), resp.Status, string(body))
	}
	return false, err
}
