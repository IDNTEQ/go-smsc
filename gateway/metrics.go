package gateway

import "github.com/prometheus/client_golang/prometheus"

// Metrics holds all Prometheus metrics for the SMSC Gateway.
type Metrics struct {
	NorthboundConnections prometheus.Gauge
	SubmitTotal           *prometheus.CounterVec
	DLRTotal              *prometheus.CounterVec
	MOTotal               *prometheus.CounterVec
	AffinityTableSize     prometheus.Gauge
	CorrelationTableSize  prometheus.Gauge
	StoreMessages         prometheus.Gauge
	RetryQueueSize        prometheus.Gauge
	SubmitLatency         prometheus.Histogram
	DeliverLatency        prometheus.Histogram
	ThrottledTotal        prometheus.Counter
	BlacklistedTotal      prometheus.Counter
	SyntheticDLRTotal     prometheus.Counter
	SubmitRetryTotal      prometheus.Counter

	// REST API callbacks
	CallbackTotal *prometheus.CounterVec // labels: status (success, retry, failed)

	// Routing
	RouteResolutions *prometheus.CounterVec // labels: type (mt, mo), result (routed, fallback, no_route)

	// Pools
	PoolHealthGauge *prometheus.GaugeVec // labels: pool_name

	// Admin
	AdminLoginTotal *prometheus.CounterVec // labels: status (success, failed)
}

// NewMetrics creates and registers all gateway metrics.
func NewMetrics() *Metrics {
	m := &Metrics{
		NorthboundConnections: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "smscgw_northbound_connections",
			Help: "Number of active engine SMPP connections",
		}),
		SubmitTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "smscgw_submit_total",
			Help: "Total submit_sm messages forwarded",
		}, []string{"status"}),
		DLRTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "smscgw_dlr_total",
			Help: "Total DLR deliver_sm messages received",
		}, []string{"status", "routed"}),
		MOTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "smscgw_mo_total",
			Help: "Total MO deliver_sm messages received",
		}, []string{"routed"}),
		AffinityTableSize: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "smscgw_affinity_table_size",
			Help: "Number of MSISDN affinity entries",
		}),
		CorrelationTableSize: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "smscgw_correlation_table_size",
			Help: "Number of pending SMPP correlations",
		}),
		StoreMessages: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "smscgw_store_messages",
			Help: "Number of messages in Pebble store",
		}),
		RetryQueueSize: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "smscgw_retry_queue_size",
			Help: "Number of messages pending retry",
		}),
		SubmitLatency: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "smscgw_submit_latency_seconds",
			Help:    "End-to-end submit_sm latency through gateway",
			Buckets: []float64{0.0001, 0.0005, 0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1.0},
		}),
		DeliverLatency: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "smscgw_deliver_latency_seconds",
			Help:    "DLR/MO routing latency",
			Buckets: []float64{0.0001, 0.0005, 0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1.0},
		}),
		ThrottledTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "smscgw_throttled_total",
			Help: "Total submit_sm messages throttled by rate limiter",
		}),
		BlacklistedTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "smscgw_blacklisted_total",
			Help: "Total submit_sm messages rejected by MSISDN blacklist",
		}),
		SyntheticDLRTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "smscgw_synthetic_dlr_total",
			Help: "Total synthetic failure DLRs generated after exhausting retries",
		}),
		SubmitRetryTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "smscgw_submit_retry_total",
			Help: "Total southbound submit retries attempted",
		}),
		CallbackTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "smscgw_callback_total",
			Help: "Total DLR/MO callback delivery attempts",
		}, []string{"status"}),
		RouteResolutions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "smscgw_route_resolutions_total",
			Help: "Total route table resolution outcomes",
		}, []string{"type", "result"}),
		PoolHealthGauge: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "smscgw_pool_healthy",
			Help: "Whether a southbound pool is healthy (1) or not (0)",
		}, []string{"pool_name"}),
		AdminLoginTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "smscgw_admin_login_total",
			Help: "Total admin login attempts",
		}, []string{"status"}),
	}

	prometheus.MustRegister(
		m.NorthboundConnections,
		m.SubmitTotal,
		m.DLRTotal,
		m.MOTotal,
		m.AffinityTableSize,
		m.CorrelationTableSize,
		m.StoreMessages,
		m.RetryQueueSize,
		m.SubmitLatency,
		m.DeliverLatency,
		m.ThrottledTotal,
		m.BlacklistedTotal,
		m.SyntheticDLRTotal,
		m.SubmitRetryTotal,
		m.CallbackTotal,
		m.RouteResolutions,
		m.PoolHealthGauge,
		m.AdminLoginTotal,
	)

	return m
}
