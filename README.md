# Tool to crate tickets in JIRA from Prometheus Alerts

This tool is for creating ticket from Prometheus alerts. It uses golang binary to run as daemon and lesten to incoming webhooks from prometheus alertmanager and create appropriate request to JIRA for creating alert in neede project. This tool is modified from https://github.com/free/jiralert . Thanks to Alin Sinpalean for original project.


### Values

- `JIRA_USER` - user to conenct and create tickets in JIRA, nust have prevelige to list and create tickets 
- `JIRA_PASS` - user pass to connect to JIRA


### Tool Run Keys

- `alsologtostderr` - log to standard error as well as files (default true)
- `config` - string The JIRAlert configuration file (default `config/jiralert.yml`)
- `listen-address` - string The address to listen on for HTTP requests. (default `:9097`)
- `log_backtrace_at` value - when logging hits line file:N, emit a stack trace
- `log_dir` - string If non-empty, write log files in this directory
- `logtostderr` - log to standard error instead of files
- `stderrthreshold` - value logs at or above this threshold go to stderr
- `v` - value log level for V logs
- `vmodule` - value comma-separated list of pattern=N settings for file-filtered logging


### How its working

1) Prometheus from alert rules rigger the alert
2) Alertmanager catch alert from Prometheus and send to jira-alerter. IMPORTANT - alertmanager reciever name must be the same as jira-alerter reciever name
3) jira-alerter catch alert from Alertmanager and send to JIRA.
4) jira-alerter create `alert_hash` using sha1 from summary field in JIRA (can be modified in jiralert.tmpl) and save this hash in JIRA ticket description.
5) jira-alerter check if alert with same `alert_hash` already exist. If exist do nothing. If not exist create new issue on JIRA. It is possible to reopen ticket if its resolved, to do that you need to define `reopen_state`  in config file `config/jiralert.yml` and this reopen state need to exist in JIRA.


### How to use

run container like this 
```
docker run --env JIRA_PASS=test-123 --env JIRA_USER=test-user --name jira-alerter --network host -p 9097:9097 --rm -v "/tmp/config:/config" jira-alerter /jira-alerter -v 1
``` 
then it will listen on port 9097 and recieve any request

you can test by tunning this request 
```
curl -L -v -H "Content-type: application/json" -X POST -d '{"receiver": "jira-des", "status": "firing", "alerts": [{"status": "firing", "labels": {"alertname": "TestAlert", "key": "value2","key4": "value4"} }], "groupLabels": {"alertname": "TestAlert20", "ggg": "gg"}}' http://localhost:9097/alert
```

all configs are commented so you can see what options mean.
