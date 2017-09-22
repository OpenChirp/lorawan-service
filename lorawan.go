package main

import (
	"fmt"
	"strings"

	"github.com/openchirp/framework"
	"github.com/openchirp/framework/pubsub"
	"github.com/openchirp/framework/rest"
)

const (
	LorawanClassA LorawanClass = iota
	LorawanClassB
	LorawanClassC
	LorawanClassUnknown
)

type LorawanClass int

func (class LorawanClass) String() string {
	switch class {
	case LorawanClassA:
		return "A"
	case LorawanClassB:
		return "B"
	case LorawanClassC:
		return "C"
	default:
		return "Unknown"
	}
}

func LorawanClassFromString(str string) LorawanClass {
	switch strings.ToUpper(str) {
	case "A":
		return LorawanClassC
	case "B":
		return LorawanClassC
	case "C":
		return LorawanClassC
	default:
		return LorawanClassUnknown
	}
}

type LorawanDeviceConfig struct {
	ID    string
	Topic string // OpenChirp MQTT Topic
	// Maybe have the device owner in the future
	Name  string
	Owner string

	DevEUI string
	AppEUI string
	AppKey string
	Class  LorawanClass
}

func (d LorawanDeviceConfig) String() string {
	return fmt.Sprintf("ID: %s, DevEUI: %s, AppEUI: %s, AppKey: %s, Name: %s, Owner: %s, Class: %v",
		d.ID,
		d.DevEUI,
		d.AppEUI,
		d.AppKey,
		d.Name,
		d.Owner)
}

func (d LorawanDeviceConfig) GetDescription() string {
	return fmt.Sprintf("%s - %s - %s", d.Name, d.Owner, d.ID)
}

func Config2Map(config []rest.KeyValuePair) map[string]string {
	m := make(map[string]string)
	for _, c := range config {
		m[c.Key] = c.Value
	}
	return m
}

type LorawanManager struct {
	bridge pubsub.Bridge
	app    AppServer
}

// Sync synchronizes the LoRa App server to the given device updates
func (l *LorawanManager) Sync(updates []framework.DeviceUpdate) error {

}

func (l *LorawanManager) Process(update framework.DeviceUpdate) error {

}
