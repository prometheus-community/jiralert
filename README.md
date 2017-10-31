# JIRAlert
JIRA integration for [Prometheus Alertmanager](https://github.com/prometheus/alertmanager).

## Overview

JIRAlert implements Alertmanager's webhook HTTP API and creates highly configurable JIRA issues when notified. One issue
is created per distinct group key — as defined by the [`group_by`](https://prometheus.io/docs/alerting/configuration/#<route>)
parameter of Alertmanager's `route` configuration section — but not closed when the alert is resolved. The expectation
is that a human will look at the issue, take any necessary action, then close it.  If no human interaction is necessary
then it should probably not alert in the first place.

If a corresponding JIRA issue already exists but is resolved, it is reopened. A JIRA transition must exist between the
resolved state and the reopened state — as defined by `reopen_state` — or reopening will fail. Optionally a "won't
fix" resolution — defined by `wont_fix_resolution` — may be defined: a JIRA issue with this resolution will not be
reopened by JIRAlert.

## Usage

Get JIRAlert, either as a [packaged release](https://github.com/alin-sinpalean/jiralert/releases) or build it yourself:

```
$ go get github.com/alin-sinpalean/jiralert/cmd/jiralert
```

then run it from the command line:

```
$ jiralert
```

Use the `-help` flag to get help information.

```
$ jiralert -help
Usage of jiralert:
  -config string
      The JIRAlert configuration file (default "config/jiralert.yml")
  -listen-address string
      The address to listen on for HTTP requests. (default ":2197")
  [...]
```

## Testing

JIRAlert expects a JSON object from Alertmanager. The format of this JSON is described in the [Alertmanager
documentation](https://prometheus.io/docs/alerting/configuration/#<webhook_config>) or, alternatively,
in the [Alertmanager GoDoc](https://godoc.org/github.com/prometheus/alertmanager/template#Data).

To quickly test if JIRAlert is working you can run:

```bash
$ curl -H "Content-type: application/json" -X POST \
  -d '{"receiver": "jira-ab", "status": "firing", "alerts": [{"status": "firing", "labels": {"alertname": "TestAlert", "key": "value"} }], "groupLabels": {"alertname": "TestAlert"}}' \
  http://localhost:2197/alert
```

## Configuration

The configuration file is essentially a list of receivers matching 1-1 all Alertmanager receivers pointing to JIRAlert
plus defaults (also as a receiver) and a pointer to the template file.

Each receiver must have a unique name (matching the Alertmanager receiver name), JIRA API access fields (URL, username
and password), a handful of required issue fields (such as the JIRA project and issue summary), some optional issue
fields (e.g. priority) and a `fields` map for other (standard or custom) JIRA fields. Most of these may use [Go
templating](https://golang.org/pkg/text/template/) to generate the actual field values based on the contents of the
Alertmanager notification. The exact same data structures and functions as those defined in the [Alertmanager template
reference](https://prometheus.io/docs/alerting/notifications/) are available in JIRAlert.

## Alertmanager configuration

To enable Alertmanager to talk to JIRAlert you need to configure a webhook in Alertmanager. You can do that by adding a
webhook receiver to your Alertmanager configuration. 

```yaml
receivers:
- name: 'jira-ab'
  webhook_configs:
  - url: 'http://localhost:2197/alert'
    # JIRAlert ignores resolved alerts, avoid unnecessary noise
    send_resolved: false
```

## License

JIRAlert is licensed under the [MIT License](https://github.com/alin-sinpalean/jiralert/blob/master/LICENSE).
Copyright (c) 2017, Alin Sinpalean
