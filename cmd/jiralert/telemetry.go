package main

import "github.com/prometheus/client_golang/prometheus"

var (
	requestTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "jiralert_requests_total",
			Help: "Requests processed, by receiver and status code.",
		},
		[]string{"receiver", "code"},
	)
)

func init() {
	prometheus.MustRegister(requestTotal)
}
