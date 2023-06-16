package config

import (
	"encoding/json"
	"log"
	"os"
)

type ProxyConfig struct {
	Host     string `json:"host"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type Channel struct {
	Name             string       `json:"name"`
	URL              string       `json:"url"`
	Exec             string       `json:"exec"`
	ProxyConfig      *ProxyConfig `json:"proxy"`
	DisableTranscode bool         `json:"disableTranscode"`
}

var (
	channels []Channel
)

func Channels() []Channel {
	return channels
}

func GetChannel(index int) *Channel {
	return &channels[index]
}

func init() {
	file, err := os.Open("channels.json")
	if err != nil {
		log.Fatal(err)
	}

	defer file.Close()

	var decoder = json.NewDecoder(file)
	err = decoder.Decode(&channels)

	if err != nil {
		log.Fatal(err)
	}
}
