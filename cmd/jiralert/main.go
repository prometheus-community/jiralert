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

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strconv"

	"github.com/andygrunwald/go-jira"
	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/prometheus-community/jiralert/pkg/alertmanager"
	"github.com/prometheus-community/jiralert/pkg/config"
	"github.com/prometheus-community/jiralert/pkg/notify"
	"github.com/prometheus-community/jiralert/pkg/template"

	_ "net/http/pprof"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	unknownReceiver             = "<unknown>"
	logFormatLogfmt             = "logfmt"
	logFormatJSON               = "json"
	defaultMaxDescriptionLength = 32767 // https://jira.atlassian.com/browse/JRASERVER-64351
)

var (
	listenAddress = flag.String("listen-address", ":9097", "The address to listen on for HTTP requests.")
	configFile    = flag.String("config", "config/jiralert.yml", "The JIRAlert configuration file")
	logLevel      = flag.String("log.level", "info", "Log filtering level (debug, info, warn, error)")
	logFormat     = flag.String("log.format", logFormatLogfmt, "Log format to use ("+logFormatLogfmt+", "+logFormatJSON+")")
	hashJiraLabel = flag.Bool("hash-jira-label", false, "if enabled: renames ALERT{...} to JIRALERT{...}; also hashes the key-value pairs inside of JIRALERT{...} in the created jira issue labels"+
		"- this ensures that the label text does not overflow the allowed length in jira (255)")
	updateSummary        = flag.Bool("update-summary", true, "When false, jiralert does not update the summary of the existing jira issue, even when changes are spotted.")
	updateDescription    = flag.Bool("update-description", true, "When false, jiralert does not update the description of the existing jira issue, even when changes are spotted.")
	reopenTickets        = flag.Bool("reopen-tickets", true, "When false, jiralert does not reopen tickets.")
	maxDescriptionLength = flag.Int("max-description-length", defaultMaxDescriptionLength, "Maximum length of Descriptions. Truncate to this size avoid server errors.")

	// Version is the build version, set by make to latest git tag/hash via `-ldflags "-X main.Version=$(VERSION)"`.
	Version = "<local build>"
)

func main() {
	if os.Getenv("DEBUG") != "" {
		runtime.SetBlockProfileRate(1)
		runtime.SetMutexProfileFraction(1)
	}

	flag.Parse()

	var logger = setupLogger(*logLevel, *logFormat)
	level.Info(logger).Log("msg", "starting JIRAlert", "version", Version)

	if !*hashJiraLabel {
		level.Warn(logger).Log("msg", "Using deprecated jira label generation - "+
			"please read https://github.com/prometheus-community/jiralert/pull/79 "+
			"and try -hash-jira-label")
	}

	config, _, err := config.LoadFile(*configFile, logger)
	if err != nil {
		level.Error(logger).Log("msg", "error loading configuration", "path", *configFile, "err", err)
		os.Exit(1)
	}

	tmpl, err := template.LoadTemplate(config.Template, logger)
	if err != nil {
		level.Error(logger).Log("msg", "error loading templates", "path", config.Template, "err", err)
		os.Exit(1)
	}

	http.HandleFunc("/alert", func(w http.ResponseWriter, req *http.Request) {
		level.Debug(logger).Log("msg", "handling /alert webhook request")
		defer func() { _ = req.Body.Close() }()

		// https://godoc.org/github.com/prometheus/alertmanager/template#Data
		data := alertmanager.Data{}
		if err := json.NewDecoder(req.Body).Decode(&data); err != nil {
			errorHandler(w, http.StatusBadRequest, err, unknownReceiver, &data, logger)
			return
		}

		conf := config.ReceiverByName(data.Receiver)
		if conf == nil {
			errorHandler(w, http.StatusNotFound, fmt.Errorf("receiver missing: %s", data.Receiver), unknownReceiver, &data, logger)
			return
		}
		level.Debug(logger).Log("msg", "  matched receiver", "receiver", conf.Name)

		// TODO: Consider reusing notifiers or just jira clients to reuse connections.
		var client *jira.Client
		var err error
		if conf.User != "" && conf.Password != "" {
			tp := jira.BasicAuthTransport{
				Username: conf.User,
				Password: string(conf.Password),
			}
			client, err = jira.NewClient(tp.Client(), conf.APIURL)
		} else if conf.PersonalAccessToken != "" {
			tp := jira.PATAuthTransport{
				Token: string(conf.PersonalAccessToken),
			}
			client, err = jira.NewClient(tp.Client(), conf.APIURL)
		}

		if err != nil {
			errorHandler(w, http.StatusInternalServerError, err, conf.Name, &data, logger)
			return
		}

		if retry, err := notify.NewReceiver(logger, conf, tmpl, client.Issue).Notify(&data, *hashJiraLabel, *updateSummary, *updateDescription, *reopenTickets, *maxDescriptionLength); err != nil {
			var status int
			if retry {
				// Instruct Alertmanager to retry.
				status = http.StatusServiceUnavailable
			} else {
				// Inaccurate, just letting Alertmanager know that it should not retry.
				status = http.StatusBadRequest
			}
			errorHandler(w, status, err, conf.Name, &data, logger)
			return
		}
		requestTotal.WithLabelValues(conf.Name, "200").Inc()

	})

	http.HandleFunc("/", HomeHandlerFunc())
	http.HandleFunc("/config", ConfigHandlerFunc(config))
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "OK", http.StatusOK) })
	http.Handle("/metrics", promhttp.Handler())

	if os.Getenv("PORT") != "" {
		*listenAddress = ":" + os.Getenv("PORT")
	}

	level.Info(logger).Log("msg", "listening", "address", *listenAddress)
	err = http.ListenAndServe(*listenAddress, nil)
	if err != nil {
		level.Error(logger).Log("msg", "failed to start HTTP server", "address", *listenAddress)
		os.Exit(1)
	}
}

func errorHandler(w http.ResponseWriter, status int, err error, receiver string, data *alertmanager.Data, logger log.Logger) {
	w.WriteHeader(status)

	response := struct {
		Error   bool
		Status  int
		Message string
	}{
		true,
		status,
		err.Error(),
	}
	// JSON response
	bytes, _ := json.Marshal(response)
	json := string(bytes[:])
	fmt.Fprint(w, json)

	level.Error(logger).Log("msg", "error handling request", "statusCode", status, "statusText", http.StatusText(status), "err", err, "receiver", receiver, "groupLabels", data.GroupLabels)
	requestTotal.WithLabelValues(receiver, strconv.FormatInt(int64(status), 10)).Inc()
}

func setupLogger(lvl string, fmt string) (logger log.Logger) {
	var filter level.Option
	switch lvl {
	case "error":
		filter = level.AllowError()
	case "warn":
		filter = level.AllowWarn()
	case "debug":
		filter = level.AllowDebug()
	default:
		filter = level.AllowInfo()
	}

	if fmt == logFormatJSON {
		logger = log.NewJSONLogger(log.NewSyncWriter(os.Stderr))
	} else {
		logger = log.NewLogfmtLogger(log.NewSyncWriter(os.Stderr))
	}
	logger = level.NewFilter(logger, filter)
	logger = log.With(logger, "ts", log.DefaultTimestampUTC, "caller", log.DefaultCaller)
	return
}
