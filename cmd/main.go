package main

import (
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/duncanleo/plex-dvr-hls/config"
	"github.com/duncanleo/plex-dvr-hls/routes"
	"github.com/gin-gonic/gin"
)

func main() {
	var port = 5004
	var portStr = os.Getenv("PORT")
	var err error

	if len(portStr) > 0 {
		port, err = strconv.Atoi(portStr)
		if err != nil {
			log.Fatal(err)
		}
	}

	r := gin.New()
	r.Use(gin.LoggerWithConfig(gin.LoggerConfig{
		SkipPaths: []string{
			"/capability",
			"/discover.json",
			"/lineup_status.json",
		},
	}))
	r.SetTrustedProxies(nil)

	r.GET("/capability", routes.Capability)
	r.GET("/discover.json", routes.Discover)
	r.GET("/lineup.json", routes.Lineup)
	r.GET("/lineup_status.json", routes.LineupStatus)
	r.GET("/stream/:channelID", routes.Stream)
	r.GET("/xmltv", routes.XMLTV)

	cfg := config.Cfg()
	log.Printf("Starting '%s' tuner with encoder profile %s\n", cfg.Name, cfg.GetEncoderProfile())

	r.Run(fmt.Sprintf(":%d", port))
}
