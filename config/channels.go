package config

import (
	"encoding/json"
	"log"
	"os"
	"sync"
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
	channels      []Channel
	channelsMutex sync.Mutex
)

func Channels() []Channel {
	channelsMutex.Lock()
	defer channelsMutex.Unlock()

	return channels
}

func GetChannel(index int) *Channel {
	channelsMutex.Lock()
	defer channelsMutex.Unlock()

	return &channels[index]
}

func init() {
	err := updateChannels()
	if err != nil {
		log.Fatal(err)
	}
}

func updateChannels() error {
	file, err := os.Open("channels.json")
	if err != nil {
		return err
	}

	defer file.Close()

	var decoder = json.NewDecoder(file)
	var newchannels []Channel
	err = decoder.Decode(&newchannels)
	if err != nil {
		return err
	}

	channelsMutex.Lock()
	defer channelsMutex.Unlock()
	channels = newchannels
	return nil
}
