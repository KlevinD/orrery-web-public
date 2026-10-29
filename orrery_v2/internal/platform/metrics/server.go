package metrics

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

func NewServer(addr string, gatherer prometheus.Gatherer) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           NewHandler(gatherer),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
}
