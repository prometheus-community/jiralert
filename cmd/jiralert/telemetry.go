package main

import "github.com/prometheus/client_golang/prometheus"

var (
	requestTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "jiralert_requests_total",
			Help: "Requests processed, by status code and provider.",
		},
		[]string{"code", "provider"},
	)
)

func init() {
	prometheus.MustRegister(requestTotal)
}
