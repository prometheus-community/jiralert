package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strconv"

	"github.com/free/jiralert/pkg/alertmanager"
	"github.com/free/jiralert/pkg/config"
	"github.com/free/jiralert/pkg/notify"
	"github.com/free/jiralert/pkg/template"
	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"

	_ "net/http/pprof"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	unknownReceiver = "<unknown>"
	logFormatLogfmt = "logfmt"
	logFormatJson = "json"
)

var (
	listenAddress = flag.String("listen-address", ":9097", "The address to listen on for HTTP requests.")
	configFile    = flag.String("config", "config/jiralert.yml", "The JIRAlert configuration file")
	logLevel      = flag.String("log.level", "info", "Log filtering level")
	logFormat     = flag.String("log.format", logFormatLogfmt, "Log format to use")

	// Version is the build version, set by make to latest git tag/hash via `-ldflags "-X main.Version=$(VERSION)"`.
	Version = "<local build>"
)

func main() {
	if os.Getenv("DEBUG") != "" {
		runtime.SetBlockProfileRate(1)
		runtime.SetMutexProfileFraction(1)
	}

	flag.Parse()

	var logger log.Logger
	{
		var lvl level.Option
		switch *logLevel {
		case "error":
			lvl = level.AllowError()
		case "warn":
			lvl = level.AllowWarn()
		case "debug":
			lvl = level.AllowDebug()
		case "info":
		default:
			lvl = level.AllowInfo()
		}
		logger = log.NewLogfmtLogger(log.NewSyncWriter(os.Stderr))
		if *logFormat == logFormatJson {
			logger = log.NewJSONLogger(log.NewSyncWriter(os.Stderr))
		}
		logger = level.NewFilter(logger, lvl)

		logger = log.With(logger, "ts", log.DefaultTimestampUTC, "caller", log.DefaultCaller)
	}

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
		defer req.Body.Close()

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
		level.Debug(logger).Log("msg", "matched receiver", "receiver", conf.Name)

		// Filter out resolved alerts, not interested in them.
		alerts := data.Alerts.Firing()
		if len(alerts) < len(data.Alerts) {
			level.Warn(logger).Log("msg", "receiver should have \"send_resolved: false\" set in Alertmanager config", "receiver", conf.Name)
			data.Alerts = alerts
		}

		if len(data.Alerts) > 0 {
			r, err := notify.NewReceiver(conf, tmpl)
			if err != nil {
				errorHandler(w, http.StatusInternalServerError, err, conf.Name, &data, logger)
				return
			}
			if retry, err := r.Notify(&data, logger); err != nil {
				var status int
				if retry {
					status = http.StatusServiceUnavailable
				} else {
					status = http.StatusInternalServerError
				}
				errorHandler(w, status, err, conf.Name, &data, logger)
				return
			}
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
