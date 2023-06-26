package routes

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	bytesStreamedCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "bytes_streamed_total",
		Help: "Bytes sent to a stream.",
	}, []string{"channel"})

	streamStartCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "stream_count",
		Help: "Number of streams started.",
	}, []string{"channel"})

	streamEndCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "stream_end_count",
		Help: "Number of streams ended.",
	}, []string{"channel", "end"})

	activeStreamsGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "active_stream_count",
		Help: "Number of streams currently active.",
	}, []string{"channel"})

	streamDurationHist = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: "stream_duration_s",
		Help: "How long streams lasted, in seconds, from the first bytes sent to the last bytes sent.",
	}, []string{"channel", "end"})

	streamEndDurationHist = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: "stream_end_duration_s",
		Help: "How long after the last bytes were sent did the stream end.",
	}, []string{"channel", "end"})
)

func init() {
	prometheus.MustRegister(bytesStreamedCounter)
	prometheus.MustRegister(streamStartCounter)
	prometheus.MustRegister(streamEndCounter)
	prometheus.MustRegister(activeStreamsGauge)
	prometheus.MustRegister(streamDurationHist)
	prometheus.MustRegister(streamEndDurationHist)
}

// streamCounters updates the prometheus metrics for a stream.
//
// It is created by newStreamCounter(), which must be followed by a
// defer.counters.finished() to guarantee that counters start and end
// match and are reported properly.
//
// Before a stream end, streamCounters.end must be updated with a
// label that describes the reason why the stream ended. Ends not
// labelled will be reported as 'Unknown'.
type streamCounters struct {
	channel     string
	endCause    string
	bytes       prometheus.Counter
	firstBytes  time.Time
	lastBytes   time.Time
	finishedRun bool
}

// newStreamCounters reports the start of a stream and creates a streamCounters.
func newStreamCounters(channel string) *streamCounters {
	streamStartCounter.WithLabelValues(channel).Inc()
	activeStreamsGauge.WithLabelValues(channel).Inc()
	return &streamCounters{
		channel: channel,
		// 'Unknown' shouldn't ever be sent; always set the end before
		// returning from Stream().
		endCause: "Unknown",
		bytes:    bytesStreamedCounter.WithLabelValues(channel),
	}
}

// atEnd sets the end cause
func (c *streamCounters) atEnd(cause string) {
	c.endCause = cause
	c.finished()
}

// incBytes increments the number of bytes reported for the stream.
func (c *streamCounters) incBytes(nbytes int) {
	now := time.Now()
	if c.firstBytes.IsZero() {
		c.firstBytes = now
	}
	c.lastBytes = now
	c.bytes.Add(float64(nbytes))
}

// finished reports the end of the stream to prometheus
func (c *streamCounters) finished() {
	if c.finishedRun {
		return
	}

	c.finishedRun = true
	activeStreamsGauge.WithLabelValues(c.channel).Dec()
	if !c.firstBytes.IsZero() {
		streamDurationHist.WithLabelValues(c.channel, c.endCause).Observe(c.lastBytes.Sub(c.firstBytes).Seconds())
		streamEndDurationHist.WithLabelValues(c.channel, c.endCause).Observe(time.Since(c.lastBytes).Seconds())
	}
	streamEndCounter.WithLabelValues(c.channel, c.endCause).Inc()
}
