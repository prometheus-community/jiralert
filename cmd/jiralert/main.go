package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/alin-sinpalean/jiralert"
	"github.com/prometheus/alertmanager/template"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	listenAddress = flag.String("listen-address", ":2197", "The address to listen on for HTTP requests.")
	configFile    = flag.String("config", "config.yaml", "The configuration file")
)

func main() {
	flag.Parse()
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	config, _, err := jiralert.LoadConfigFile(*configFile)
	if err != nil {
		log.Fatalf("Configuration error: %s", err)
	}

	tmpl, err := jiralert.LoadTemplate(config.Template)
	if err != nil {
		log.Fatalf("Error loading template from %s: %s", config.Template, err)
	}

	http.HandleFunc("/alert", func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()

		// https://godoc.org/github.com/prometheus/alertmanager/template#Data
		data := template.Data{}
		if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
			errorHandler(w, http.StatusBadRequest, err, "?")
			return
		}

		receiverConf := config.ReceiverByName(data.Receiver)
		if receiverConf == nil {
			errorHandler(w, http.StatusBadRequest, fmt.Errorf("Receiver missing: %s", data.Receiver), "?")
			return
		}

		alerts := data.Alerts.Firing()
		if len(alerts) < len(data.Alerts) {
			log.Println("Debug: Please set \"send_resolved: false\" on Alertmanager receiver " + data.Receiver)
			data.Alerts = alerts
		}

		if len(alerts) > 0 {
			jira := jiralert.NewJira(receiverConf, tmpl)
			if retry, err := jira.Notify(&data); err != nil {
				var status int
				if retry {
					status = http.StatusServiceUnavailable
				} else {
					status = http.StatusInternalServerError
				}
				errorHandler(w, status, err, "JIRA")
				return
			}
		}

		requestTotal.WithLabelValues("200", receiverConf.Name).Inc()
	})

	http.Handle("/metrics", prometheus.Handler())

	if os.Getenv("PORT") != "" {
		*listenAddress = ":" + os.Getenv("PORT")
	}

	log.Fatal(http.ListenAndServe(*listenAddress, nil))
}

func errorHandler(w http.ResponseWriter, status int, err error, provider string) {
	w.WriteHeader(status)

	data := struct {
		Error   bool
		Status  int
		Message string
	}{
		true,
		status,
		err.Error(),
	}
	// respond json
	bytes, _ := json.Marshal(data)
	json := string(bytes[:])
	fmt.Fprint(w, json)

	log.Println("Error: " + json)
	requestTotal.WithLabelValues(strconv.FormatInt(int64(status), 10), provider).Inc()
}
