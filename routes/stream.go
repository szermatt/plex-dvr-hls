package routes

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/duncanleo/plex-dvr-hls/config"
	"github.com/gin-gonic/gin"
	"github.com/google/shlex"
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
)

func init() {
	prometheus.MustRegister(bytesStreamedCounter)
	prometheus.MustRegister(streamStartCounter)
	prometheus.MustRegister(streamEndCounter)
	prometheus.MustRegister(activeStreamsGauge)
	prometheus.MustRegister(streamDurationHist)
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
	channel    string
	end        string
	bytes      prometheus.Counter
	firstBytes time.Time
	lastBytes  time.Time
}

// newStreamCounter reports the start of a stream and creates a streamCounters.
func newStreamCounter(channel string) *streamCounters {
	streamStartCounter.WithLabelValues(channel).Inc()
	activeStreamsGauge.WithLabelValues(channel).Inc()
	return &streamCounters{
		channel: channel,
		// 'Unknown' shouldn't ever be sent; always set the end before
		// returning from Stream().
		end:   "Unknown",
		bytes: bytesStreamedCounter.WithLabelValues(channel),
	}
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
	activeStreamsGauge.WithLabelValues(c.channel).Dec()
	if !c.firstBytes.IsZero() {
		streamDurationHist.WithLabelValues(c.channel, c.end).Observe(c.lastBytes.Sub(c.firstBytes).Seconds())
	}
	streamEndCounter.WithLabelValues(c.channel, c.end).Inc()
}

func Stream(c *gin.Context) {
	var channelIDStr = c.Param("channelID")
	channelID, err := strconv.Atoi(channelIDStr)
	if err != nil {
		c.Status(http.StatusBadRequest)
		return
	}

	var channel = config.GetChannel(channelID - 1)
	var transcode = c.Query("transcode")

	counters := newStreamCounter(channel.Name)
	defer counters.finished()

	log.Printf("[STREAM] Starting '%s'\n", channel.Name)

	c.Header("Content-Type", "video/mp2t")

	var cmd *exec.Cmd
	cfg := config.Cfg()
	if channel.Exec != "" {
		cmd, err = execCommand(channel.Exec)
		if err != nil {
			counters.end = "ConfigurationError"
			log.Println(err)
			c.Status(http.StatusInternalServerError)
			return
		}
	} else {
		cmd = ffmpegCommand(cfg, channel, transcode)
	}
	outPipe, err := cmd.StdoutPipe()
	if err != nil {
		counters.end = "CmdPipeError"
		log.Println(err)
		c.Status(http.StatusInternalServerError)
		return
	}

	cmd.Stderr = os.Stdout

	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	err = cmd.Start()
	if err != nil {
		counters.end = "CmdStartError"
		log.Println(err)
		c.Status(http.StatusInternalServerError)
		return
	}

	buf := make([]byte, 4*1024)
	clientDisconnected := c.Stream(func(w io.Writer) bool {
		nbytes, err := outPipe.Read(buf)
		if err == io.EOF {
			counters.end = "EOF"
			log.Printf("[STREAM] '%s' end of stream\n", channel.Name)
			return false
		} else if err != nil {
			counters.end = "ReadError"
			log.Printf("[STREAM] '%s' read error: %s\n", channel.Name, err)
			return false
		}
		if nbytes > 0 {
			_, err := w.Write(buf[0:nbytes])
			if err != nil {
				counters.end = "WriteError"
				log.Printf("[STREAM] '%s' write error %s\n", channel.Name, err)
				return false
			}
			counters.incBytes(nbytes)
		}
		return true
	})
	if clientDisconnected {
		counters.end = "Disconnected"
	}
	log.Printf("[STREAM] '%s' done. client disconnected=%v\n", channel.Name, clientDisconnected)

	outPipe.Close()
	if cmd.Process != nil {
		if pgid, err := syscall.Getpgid(cmd.Process.Pid); err == nil {
			syscall.Kill(-pgid, syscall.SIGKILL)
		} else {
			cmd.Process.Kill()
		}
		cmd.Wait()
	}
}

func ffmpegCommand(cfg *config.Config, channel *config.Channel, transcode string) *exec.Cmd {
	var ffmpegArgs []string

	if channel.ProxyConfig != nil {
		ffmpegArgs = append(
			ffmpegArgs,
			"-http_proxy",
			fmt.Sprintf("http://%s:%s@%s", channel.ProxyConfig.Username, channel.ProxyConfig.Password, channel.ProxyConfig.Host),
		)
	}

	switch cfg.GetEncoderProfile() {
	case config.EncoderProfileVAAPI:
		ffmpegArgs = append(
			ffmpegArgs,
			"-vaapi_device",
			"/dev/dri/renderD128",
			"-hwaccel",
			"vaapi",
			"-hwaccel_output_format",
			"vaapi",
		)
	case config.EncoderProfileVideoToolbox:
		ffmpegArgs = append(
			ffmpegArgs,
			"-hwaccel",
			"videotoolbox",
		)
	}

	ffmpegArgs = append(
		ffmpegArgs,
		"-i",
		channel.URL,
	)

	if channel.DisableTranscode {
		ffmpegArgs = append(
			ffmpegArgs,
			"-c:v",
			"copy",
		)
	} else {
		switch cfg.GetEncoderProfile() {
		case config.EncoderProfileVideoToolbox:
			ffmpegArgs = append(
				ffmpegArgs,
				"-c:v",
				"h264_videotoolbox",
			)
			break
		case config.EncoderProfileVAAPI:
			ffmpegArgs = append(
				ffmpegArgs,
				"-c:v",
				"h264_vaapi",
				"-vf",
				"scale_vaapi=format=nv12,hwupload",
			)
			break
		case config.EncoderProfileOMX:
			ffmpegArgs = append(
				ffmpegArgs,
				"-c:v",
				"h264_omx",
			)
			break
		default:
			ffmpegArgs = append(
				ffmpegArgs,
				"-c:v",
				"libx264",
				"-preset",
				"superfast",
			)
			break
		}
	}

	ffmpegArgs = append(
		ffmpegArgs,
		"-b:a",
		"256k",
		"-copyinkf",
		"-metadata",
		"service_provider=AMAZING",
		"-metadata",
		fmt.Sprintf("service_name=%s", strings.ReplaceAll(channel.Name, " ", "")),
		"-tune",
		"zerolatency",
		"-mbd",
		"rd",
		"-flags",
		"+ilme+ildct",
		"-fflags",
		"+genpts",
	)

	switch transcode {
	case "mobile":
	case "internet720":
		ffmpegArgs = append(
			ffmpegArgs,
			"-s",
			"1280x720",
			"-r",
			"30",
		)
		break
	}

	ffmpegArgs = append(
		ffmpegArgs,
		"-f",
		"mpegts",
		"pipe:1",
	)

	return exec.Command(
		"ffmpeg",
		ffmpegArgs...,
	)
}

func execCommand(cmdLine string) (*exec.Cmd, error) {
	cmdArray, err := shlex.Split(cmdLine)
	if err != nil {
		return nil, err
	}
	return exec.Command(cmdArray[0], cmdArray[1:]...), nil
}
