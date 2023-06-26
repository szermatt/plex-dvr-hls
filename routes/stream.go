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

	"github.com/duncanleo/plex-dvr-hls/config"
	"github.com/gin-gonic/gin"
	"github.com/google/shlex"
)

func Stream(c *gin.Context) {
	var channelIDStr = c.Param("channelID")
	channelID, err := strconv.Atoi(channelIDStr)
	if err != nil {
		c.Status(http.StatusBadRequest)
		return
	}

	var channel = config.GetChannel(channelID - 1)
	var transcode = c.Query("transcode")

	counters := newStreamCounters(channel.Name)
	defer counters.finished()

	log.Printf("[STREAM] Starting '%s'\n", channel.Name)

	c.Header("Content-Type", "video/mp2t")

	var cmd *exec.Cmd
	cfg := config.Cfg()
	if channel.Exec != "" {
		cmd, err = execCommand(channel.Exec)
		if err != nil {
			counters.atEnd("ConfigurationError")
			log.Println(err)
			c.Status(http.StatusInternalServerError)
			return
		}
	} else {
		cmd = ffmpegCommand(cfg, channel, transcode)
	}
	outPipe, err := cmd.StdoutPipe()
	if err != nil {
		counters.atEnd("CmdPipeError")
		log.Println(err)
		c.Status(http.StatusInternalServerError)
		return
	}

	cmd.Stderr = os.Stdout

	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	err = cmd.Start()
	if err != nil {
		counters.atEnd("CmdStartError")
		log.Println(err)
		c.Status(http.StatusInternalServerError)
		return
	}

	buf := make([]byte, 4*1024)
	clientDisconnected := c.Stream(func(w io.Writer) bool {
		nbytes, err := outPipe.Read(buf)
		if err == io.EOF {
			counters.atEnd("EOF")
			log.Printf("[STREAM] '%s' end of stream\n", channel.Name)
			return false
		} else if err != nil {
			counters.atEnd("ReadError")
			log.Printf("[STREAM] '%s' read error: %s\n", channel.Name, err)
			return false
		}
		if nbytes > 0 {
			_, err := w.Write(buf[0:nbytes])
			if err != nil {
				counters.atEnd("WriteError")
				log.Printf("[STREAM] '%s' write error %s\n", channel.Name, err)
				return false
			}
			counters.incBytes(nbytes)
		}
		return true
	})
	if clientDisconnected {
		counters.atEnd("Disconnected")
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
