package config

import (
	"encoding/json"
	"log"
	"os"
	"sync"
)

type EncoderProfile string

const (
	EncoderProfileCPU          EncoderProfile = "cpu"
	EncoderProfileVAAPI        EncoderProfile = "vaapi"
	EncoderProfileVideoToolbox EncoderProfile = "video_toolbox"
	EncoderProfileOMX          EncoderProfile = "omx"
)

type Config struct {
	Name           string          `json:"name"`
	EncoderProfile *EncoderProfile `json:"encoder_profile"`
	DeviceID       int32           `json:"deviceID"`
}

func (c Config) GetEncoderProfile() EncoderProfile {
	if c.EncoderProfile == nil {
		return EncoderProfileCPU
	}

	switch *c.EncoderProfile {
	case EncoderProfileVAAPI:
		return EncoderProfileVAAPI
	case EncoderProfileOMX:
		return EncoderProfileOMX
	case EncoderProfileVideoToolbox:
		return EncoderProfileVideoToolbox
	}

	return EncoderProfileCPU
}

var (
	cfg      *Config
	cfgMutex sync.Mutex
)

func Cfg() *Config {
	cfgMutex.Lock()
	defer cfgMutex.Unlock()

	return cfg
}

func init() {
	err := updateConfig()
	if err != nil {
		log.Fatal(err)
	}
}

func updateConfig() error {
	file, err := os.Open("config.json")
	if err != nil {
		log.Fatal(err)
	}

	defer file.Close()

	var decoder = json.NewDecoder(file)
	var newcfg Config
	err = decoder.Decode(&newcfg)

	if err != nil {
		return err
	}

	cfgMutex.Lock()
	defer cfgMutex.Unlock()
	cfg = &newcfg
	return nil
}
