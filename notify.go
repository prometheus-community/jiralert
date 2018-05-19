package jiralert

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net/http"
	"reflect"
	"strings"

	"github.com/andygrunwald/go-jira"
	"github.com/free/jiralert/alertmanager"
	log "github.com/golang/glog"
	"github.com/trivago/tgo/tcontainer"
)

// Receiver wraps a JIRA client corresponding to a specific Alertmanager receiver, with its configuration and templates.
type Receiver struct {
	conf   *ReceiverConfig
	tmpl   *Template
	client *jira.Client
}

// NewReceiver creates a Receiver using the provided configuration and template.
func NewReceiver(c *ReceiverConfig, t *Template) (*Receiver, error) {
	client, err := jira.NewClient(http.DefaultClient, c.APIURL)
	if err != nil {
		return nil, err
	}
	client.Authentication.SetBasicAuth(c.User, string(c.Password))

	return &Receiver{conf: c, tmpl: t, client: client}, nil
}

// Notify implements the Notifier interface.
func (r *Receiver) Notify(data *alertmanager.Data) (bool, error) {
	project := r.tmpl.Execute(r.conf.Project, data)
	// check errors from r.tmpl.Execute()
	if r.tmpl.err != nil {
		return false, r.tmpl.err
	}
	// Looks like an ALERT metric name, with spaces removed.
	issueLabel := toIssueLabel(data.GroupLabels)

	issue, retry, err := r.search(project, issueLabel)
	if err != nil {
		return retry, err
	}

	if issue != nil {
		// The set of JIRA status categories is fixed, this is a safe check to make.
		if issue.Fields.Status.StatusCategory.Key != "done" {
			// Issue is in a "to do" or "in progress" state, all done here.
			log.V(1).Infof("Issue %s for %s is unresolved, nothing to do", issue.Key, issueLabel)
			return false, nil
		}
		if r.conf.WontFixResolution != "" && issue.Fields.Resolution != nil &&
			issue.Fields.Resolution.Name == r.conf.WontFixResolution {
			// Issue is resolved as "Won't Fix" or equivalent, log a message just in case.
			log.Infof("Issue %s for %s is resolved as %q, not reopening", issue.Key, issueLabel, issue.Fields.Resolution.Name)
			return false, nil
		}
		log.Infof("Issue %s for %s was resolved, reopening", issue.Key, issueLabel)
		return r.reopen(issue.Key)
	}

	log.Infof("No issue matching %s found, creating new issue", issueLabel)
	issue = &jira.Issue{
		Fields: &jira.IssueFields{
			Project:     jira.Project{Key: project},
			Type:        jira.IssueType{Name: r.tmpl.Execute(r.conf.IssueType, data)},
			Description: r.tmpl.Execute(r.conf.Description, data),
			Summary:     r.tmpl.Execute(r.conf.Summary, data),
			Labels: []string{
				issueLabel,
			},
			Unknowns: tcontainer.NewMarshalMap(),
		},
	}
	if r.conf.Priority != "" {
		issue.Fields.Priority = &jira.Priority{Name: r.tmpl.Execute(r.conf.Priority, data)}
	}

	// Add Components
	if len(r.conf.Components) > 0 {
		issue.Fields.Components = make([]*jira.Component, 0, len(r.conf.Components))
		for _, component := range r.conf.Components {
			issue.Fields.Components = append(issue.Fields.Components, &jira.Component{Name: component})
		}
	}

	// Add Labels
	if r.conf.AddGroupLabels {
		for k, v := range data.GroupLabels {
			issue.Fields.Labels = append(issue.Fields.Labels, fmt.Sprintf("%s=%q", k, v))
		}
	}

	for key, value := range r.conf.Fields {
		issue.Fields.Unknowns[key] = deepCopyWithTemplate(value, r.tmpl, data)
	}
	// check errors from r.tmpl.Execute()
	if r.tmpl.err != nil {
		return false, r.tmpl.err
	}
	retry, err = r.create(issue)
	if err == nil {
		log.Infof("Issue created: key=%s ID=%s", issue.Key, issue.ID)
	}
	return retry, err
}

// deepCopyWithTemplate returns a deep copy of a map/slice/array/string/int/bool or combination thereof, executing the
// provided template (with the provided data) on all string keys or values. All maps are connverted to
// map[string]interface{}, with all non-string keys discarded.
func deepCopyWithTemplate(value interface{}, tmpl *Template, data interface{}) interface{} {
	if value == nil {
		return value
	}

	valueMeta := reflect.ValueOf(value)
	switch valueMeta.Kind() {

	case reflect.String:
		return tmpl.Execute(value.(string), data)

	case reflect.Array, reflect.Slice:
		arrayLen := valueMeta.Len()
		converted := make([]interface{}, arrayLen)
		for i := 0; i < arrayLen; i++ {
			converted[i] = deepCopyWithTemplate(valueMeta.Index(i).Interface(), tmpl, data)
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
			strKey = tmpl.Execute(strKey, data)
			converted[strKey] = deepCopyWithTemplate(valueMeta.MapIndex(keyMeta).Interface(), tmpl, data)
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

func (r *Receiver) search(project, issueLabel string) (*jira.Issue, bool, error) {
	query := fmt.Sprintf("project=%s and labels=%q order by key", project, issueLabel)
	options := &jira.SearchOptions{
		Fields:     []string{"summary", "status", "resolution"},
		MaxResults: 50,
	}
	log.V(1).Infof("search: query=%v options=%+v", query, options)
	issues, resp, err := r.client.Issue.Search(query, options)
	if err != nil {
		retry, err := handleJiraError("Issue.Search", resp, err)
		return nil, retry, err
	}
	if len(issues) > 0 {
		if len(issues) > 1 {
			// Swallow it, but log an error.
			log.Errorf("More than one issue matched %s, will only update first: %+v", query, issues)
		}
		log.V(1).Infof("  found: %+v", issues[0])
		return &issues[0], false, nil
	}
	log.V(1).Infof("  no results")
	return nil, false, nil
}

func (r *Receiver) reopen(issueKey string) (bool, error) {
	transitions, resp, err := r.client.Issue.GetTransitions(issueKey)
	if err != nil {
		return handleJiraError("Issue.GetTransitions", resp, err)
	}
	for _, t := range transitions {
		if t.Name == r.conf.ReopenState {
			log.V(1).Infof("reopen: issueKey=%v transitionID=%v", issueKey, t.ID)
			resp, err = r.client.Issue.DoTransition(issueKey, t.ID)
			if err != nil {
				return handleJiraError("Issue.DoTransition", resp, err)
			}
			log.V(1).Infof("  done")
			return false, nil
		}
	}
	return false, fmt.Errorf("JIRA state %q does not exist or no transition possible for %s", r.conf.ReopenState, issueKey)
}

func (r *Receiver) create(issue *jira.Issue) (bool, error) {
	log.V(1).Infof("create: issue=%v", *issue)
	issue, resp, err := r.client.Issue.Create(issue)
	if err != nil {
		return handleJiraError("Issue.Create", resp, err)
	}

	log.V(1).Infof("  done: key=%s ID=%s", issue.Key, issue.ID)
	return false, nil
}

func handleJiraError(api string, resp *jira.Response, err error) (bool, error) {
	if resp == nil || resp.Request == nil {
		log.V(1).Infof("handleJiraError: api=%s, err=%s", api, err)
	} else {
		log.V(1).Infof("handleJiraError: api=%s, url=%s, err=%s", api, resp.Request.URL, err)
	}

	if resp != nil && resp.StatusCode/100 != 2 {
		retry := resp.StatusCode == 500 || resp.StatusCode == 503
		body, _ := ioutil.ReadAll(resp.Body)
		// go-jira error message is not particularly helpful, replace it
		return retry, fmt.Errorf("JIRA request %s returned status %s, body %q", resp.Request.URL, resp.Status, string(body))
	}
	return false, fmt.Errorf("JIRA request %s failed: %s", api, err)
}
