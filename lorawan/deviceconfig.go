package lorawan

import (
	"fmt"
	"strings"

	"github.com/openchirp/framework"
)

type LorawanClass int

const (
	LorawanClassA LorawanClass = iota
	LorawanClassB
	LorawanClassC
	LorawanClassUnknown
)

func (class LorawanClass) String() string {
	switch class {
	case LorawanClassA:
		return "A"
	case LorawanClassB:
		// return "B"
		return "A" // currently map B onto A
	case LorawanClassC:
		return "C"
	default:
		return "Unknown"
	}
}

func LorawanClassFromString(str string) LorawanClass {
	switch strings.ToUpper(str) {
	case "A":
		return LorawanClassA
	case "B":
		return LorawanClassB
	case "C":
		return LorawanClassC
	default:
		return LorawanClassUnknown
	}
}

type DeviceConfig struct {
	// OC Device ID
	ID string
	// OC MQTT Topic
	Topic string
	// OC Device Nname
	Name string
	// OC Device Owner
	Owner string

	DevEUI string
	AppEUI string
	AppKey string
	Class  LorawanClass
}

func NewDeviceConfig(update framework.DeviceUpdate) (DeviceConfig, error) {
	var config DeviceConfig
	// update.Config["DevEUI"]

	return config, nil
}

func (d DeviceConfig) String() string {
	return fmt.Sprintf("ID: %s, DevEUI: %s, AppEUI: %s, AppKey: %s, Name: %s, Owner: %s, Class: %v",
		d.ID,
		d.DevEUI,
		d.AppEUI,
		d.AppKey,
		d.Name,
		d.Owner,
		d.Class)
}

func (d DeviceConfig) CheckParameters() string {
	// TODO: Implement parameter checking
	// Should return message indicating problem if there is one
	if len(d.DevEUI) != 16 {
		return "Error - DevEUI must have 16 hex characters"
	}
	if len(d.AppEUI) != 16 {
		return "Error - AppEUI must have 16 hex characters"
	}
	if len(d.AppKey) != 32 {
		return "Error - AppKey must have 32 hex characters"
	}
	return ""
}

func (d DeviceConfig) GetDescription() string {
	return fmt.Sprintf("%s - %s", d.Name, d.Owner)
}
