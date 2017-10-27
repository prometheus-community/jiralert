package jiralert

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"

	"github.com/andygrunwald/go-jira"
	"github.com/prometheus/alertmanager/template"
	"github.com/prometheus/alertmanager/types"
	"github.com/prometheus/common/log"
	"github.com/trivago/tgo/tcontainer"
	"golang.org/x/net/context"
)

type Jira struct {
	conf *JiraConfig
	tmpl *template.Template
}

func NewJira(c *JiraConfig, t *template.Template) *Jira {
	return &Jira{conf: c, tmpl: t}
}

// Notify implements the Notifier interface.
func (n *Jira) Notify(ctx context.Context, as ...*types.Alert) (bool, error) {
	client, err := jira.NewClient(http.DefaultClient, n.conf.APIURL)
	if err != nil {
		return false, err
	}
	client.Authentication.SetBasicAuth(n.conf.User, string(n.conf.Password))

	data := n.tmpl.Data(receiverName(ctx), groupLabels(ctx), as...)
	tmpl := tmplText(n.tmpl, data, &err)

	project := tmpl(n.conf.Project)
	// check errors from tmpl()
	if err != nil {
		return false, err
	}
	// Looks like an ALERT metric name, with spaces removed.
	issueLabel := "ALERT" + strings.Replace(groupLabels(ctx).String(), " ", "", -1)
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
			Type:        jira.IssueType{Name: tmpl(n.conf.IssueType)},
			Description: tmpl(n.conf.Description),
			Summary:     tmpl(n.conf.Summary),
			Labels: []string{
				issueLabel,
			},
			Unknowns: tcontainer.NewMarshalMap(),
		},
	}
	if n.conf.Priority != "" {
		issue.Fields.Priority = &jira.Priority{Name: tmpl(n.conf.Priority)}
	}
	for key, value := range n.conf.Fields {
		issue.Fields.Unknowns[key] = tmpl(fmt.Sprint(value))
	}
	// check errors from tmpl()
	if err != nil {
		return false, err
	}
	return n.create(client, issue)
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
