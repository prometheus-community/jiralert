package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strconv"

	"github.com/free/jiralert"
	"github.com/free/jiralert/alertmanager"
	log "github.com/golang/glog"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	_ "net/http/pprof"
)

const (
	unknownReceiver = "<unknown>"
)

var (
	listenAddress = flag.String("listen-address", ":9097", "The address to listen on for HTTP requests.")
	configFile    = flag.String("config", "config/jiralert.yml", "The JIRAlert configuration file")

	// Version is the build version, set by make to latest git tag/hash via `-ldflags "-X main.Version=$(VERSION)"`.
	Version = "<local build>"
)

func main() {
	if os.Getenv("DEBUG") != "" {
		runtime.SetBlockProfileRate(1)
		runtime.SetMutexProfileFraction(1)
	}

	// Override -alsologtostderr default value.
	if alsoLogToStderr := flag.Lookup("alsologtostderr"); alsoLogToStderr != nil {
		alsoLogToStderr.DefValue = "true"
		alsoLogToStderr.Value.Set("true")
	}
	flag.Parse()

	log.Infof("Starting JIRAlert version %s", Version)

	config, _, err := jiralert.LoadConfigFile(*configFile)
	if err != nil {
		log.Fatalf("Error loading configuration: %s", err)
	}

	tmpl, err := jiralert.LoadTemplate(config.Template)
	if err != nil {
		log.Fatalf("Error loading templates from %s: %s", config.Template, err)
	}

	http.HandleFunc("/alert", func(w http.ResponseWriter, req *http.Request) {
		log.V(1).Infof("Handling /alert webhook request")
		defer req.Body.Close()

		// https://godoc.org/github.com/prometheus/alertmanager/template#Data
		data := alertmanager.Data{}
		if err := json.NewDecoder(req.Body).Decode(&data); err != nil {
			errorHandler(w, http.StatusBadRequest, err, unknownReceiver, &data)
			return
		}

		conf := config.ReceiverByName(data.Receiver)
		if conf == nil {
			errorHandler(w, http.StatusNotFound, fmt.Errorf("Receiver missing: %s", data.Receiver), unknownReceiver, &data)
			return
		}
		log.V(1).Infof("Matched receiver: %q", conf.Name)

		// Filter out resolved alerts, not interested in them.
		alerts := data.Alerts.Firing()
		if len(alerts) < len(data.Alerts) {
			log.Warningf("Please set \"send_resolved: false\" on receiver %s in the Alertmanager config", conf.Name)
			data.Alerts = alerts
		}

		if len(data.Alerts) > 0 {
			r, err := jiralert.NewReceiver(conf, tmpl)
			if err != nil {
				errorHandler(w, http.StatusInternalServerError, err, conf.Name, &data)
				return
			}
			if retry, err := r.Notify(&data); err != nil {
				var status int
				if retry {
					status = http.StatusServiceUnavailable
				} else {
					status = http.StatusInternalServerError
				}
				errorHandler(w, status, err, conf.Name, &data)
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

	log.Infof("Listening on %s", *listenAddress)
	log.Fatal(http.ListenAndServe(*listenAddress, nil))
}

func errorHandler(w http.ResponseWriter, status int, err error, receiver string, data *alertmanager.Data) {
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

	log.Errorf("%d %s: err=%s receiver=%q groupLabels=%+v", status, http.StatusText(status), err, receiver, data.GroupLabels)
	requestTotal.WithLabelValues(receiver, strconv.FormatInt(int64(status), 10)).Inc()
}
