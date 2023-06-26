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

	streaming := false
	fail := func(cause string, err error) {
		counters.atEnd(cause)
		log.Printf("[STREAM] '%s' %s: %s\n", channel.Name, cause, err)
		if !streaming {
			c.Status(http.StatusInternalServerError)
		}
	}
	log.Printf("[STREAM] Starting '%s'\n", channel.Name)

	c.Header("Content-Type", "video/mp2t")

	var execCmd *exec.Cmd
	cfg := config.Cfg()
	if channel.Exec != "" {
		execCmd, err = execCommand(channel.Exec)
		if err != nil {
			fail("ConfigurationError", err)
			return
		}
	}
	ffCmd := ffmpegCommand(cfg, channel, transcode)
	ffCmd.Stderr = os.Stdout

	if execCmd != nil {
		r, w := io.Pipe()
		execCmd.Stdout = w
		ffCmd.Stdin = r
		defer w.Close()
	}

	outPipe, err := ffCmd.StdoutPipe()
	if err != nil {
		fail("CmdPipeError", err)
		return
	}

	if execCmd != nil {
		execCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		err = execCmd.Start()
		if err != nil {
			fail("ExecStartError", err)
			return
		}
		defer func() {
			if execCmd.Process != nil {
				if pgid, err := syscall.Getpgid(execCmd.Process.Pid); err == nil {
					syscall.Kill(-pgid, syscall.SIGKILL)
				} else {
					execCmd.Process.Kill()
				}
			}
			execCmd.Wait()
		}()
	}

	err = ffCmd.Start()
	if err != nil {
		fail("FFMpegStartError", err)
		return
	}
	defer func() {
		if ffCmd.Process != nil {
			ffCmd.Process.Kill()
		}
		ffCmd.Wait()
	}()

	streaming = true
	buf := make([]byte, 4*1024)
	clientDisconnected := c.Stream(func(w io.Writer) bool {
		nbytes, err := outPipe.Read(buf)
		if err == io.EOF {
			fail("EOF", err)
			return false
		} else if err != nil {
			fail("ReadError", err)
			return false
		}
		if nbytes > 0 {
			_, err := w.Write(buf[0:nbytes])
			if err != nil {
				fail("WriteError", err)
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

	input := channel.URL
	if channel.Exec != "" {
		input = "-"
	}
	ffmpegArgs = append(
		ffmpegArgs,
		"-i",
		input,
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
