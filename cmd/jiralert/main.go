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
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"runtime"
	"strconv"

	"github.com/andygrunwald/go-jira"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/prometheus-community/jiralert/pkg/alertmanager"
	"github.com/prometheus-community/jiralert/pkg/config"
	"github.com/prometheus-community/jiralert/pkg/notify"
	"github.com/prometheus-community/jiralert/pkg/template"

	_ "net/http/pprof"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	unknownReceiver = "<unknown>"
	logFormatLogfmt = "logfmt"
	logFormatJSON   = "json"
)

var (
	listenAddress = flag.String("listen-address", ":9097", "The address to listen on for HTTP requests.")
	configFile    = flag.String("config", "config/jiralert.yml", "The JIRAlert configuration file")
	logLevel      = flag.String("log.level", "info", "Log filtering level (debug, info, warn, error)")
	logFormat     = flag.String("log.format", logFormatLogfmt, "Log format to use ("+logFormatLogfmt+", "+logFormatJSON+")")

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
		hc, err := createHTTPClient(conf)
		if err != nil {
			errorHandler(w, http.StatusInternalServerError, err, conf.Name, &data, logger)
			return
		}

		client, err := jira.NewClient(hc, conf.APIURL)
		if err != nil {
			errorHandler(w, http.StatusInternalServerError, err, conf.Name, &data, logger)
			return
		}

		if conf.User != "" && conf.Password != "" {
			//lint:ignore SA1019 SetBasicAuth is marked as deprecated but we can't use
			// BasicAuthTransport with custom TLS settings, like client certs.
			client.Authentication.SetBasicAuth(conf.User, string(conf.Password))
		}

		if retry, err := notify.NewReceiver(logger, conf, tmpl, client.Issue).Notify(&data); err != nil {
			var status int
			if retry {
				// Instruct Alertmanager to retry.
				status = http.StatusServiceUnavailable
			} else {
				status = http.StatusInternalServerError
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

func createHTTPClient(conf *config.ReceiverConfig) (*http.Client, error) {
	tlsConfig, err := newTLSConfig(conf)
	if err != nil {
		return nil, err
	}

	hc := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
		},
	}

	return hc, nil

}

func newTLSConfig(conf *config.ReceiverConfig) (*tls.Config, error) {
	tlsConfig := &tls.Config{
		InsecureSkipVerify: conf.InsecureSkipVerify,
		Renegotiation:      tls.RenegotiateOnceAsClient,
	}

	// Read in a CA certificate, if one is specified.
	if len(conf.CAFile) > 0 {
		b, err := readCAFile(conf.CAFile)
		if err != nil {
			return nil, err
		}
		if !updateRootCA(tlsConfig, b) {
			return nil, fmt.Errorf("unable to use specified CA certificate %s", conf.CAFile)
		}
	}

	// Configure TLS with a client certificate, if certificate and key files are specified.
	if len(conf.CertFile) > 0 && len(conf.KeyFile) == 0 {
		return nil, fmt.Errorf("client certificate file %q specified without client key file", conf.CertFile)
	}

	if len(conf.KeyFile) > 0 && len(conf.CertFile) == 0 {
		return nil, fmt.Errorf("client key file %q specified without client certificate file", conf.KeyFile)
	}

	if len(conf.CertFile) > 0 && len(conf.KeyFile) > 0 {
		cert, err := getClientCertificate(conf)
		if err != nil {
			return nil, err
		}
		tlsConfig.Certificates = []tls.Certificate{*cert}
	}

	return tlsConfig, nil
}

// readCAFile reads the CA certificate file from disk.
func readCAFile(f string) ([]byte, error) {
	data, err := ioutil.ReadFile(f)
	if err != nil {
		return nil, fmt.Errorf("unable to load specified CA certificate %s: %s", f, err)
	}
	return data, nil
}

func updateRootCA(cfg *tls.Config, b []byte) bool {
	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(b) {
		return false
	}
	cfg.RootCAs = caCertPool
	return true
}

// getClientCertificate reads the pair of client certificate and key from disk and returns a tls.Certificate.
func getClientCertificate(c *config.ReceiverConfig) (*tls.Certificate, error) {
	cert, err := tls.LoadX509KeyPair(c.CertFile, c.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("unable to use specified client certificate (%s) and key (%s): %s", c.CertFile, c.KeyFile, err)
	}
	return &cert, nil
}
