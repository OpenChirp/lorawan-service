package deviceconfig

import (
	"errors"
	"fmt"
	"strings"

	"github.com/openchirp/framework"
	"github.com/openchirp/lorawan-service/lorawan/utils"
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
		return "B"
	case LorawanClassC:
		return "C"
	default:
		return "Unknown"
	}
}

func LorawanClassFromString(str string) LorawanClass {
	switch strings.ToUpper(str) {
	case "A", "a":
		return LorawanClassA
	case "B", "b":
		return LorawanClassB
	case "C", "c":
		return LorawanClassC
	default:
		return LorawanClassUnknown
	}
}

type OCDeviceInfo struct {
	// OC Device ID
	ID string
	// OC MQTT Topic
	Topic string
	// OC Device Name
	Name string
	// OC Device Owner Name
	OwnerName string
	// OC Device Owner Email
	OwnerEmail string
}

func (ocd OCDeviceInfo) OwnerString() string {
	return ocd.OwnerEmail + "(" + ocd.OwnerName + ")"
}

type LorawanConfig struct {
	DevEUI string
	AppEUI string
	AppKey string
	Class  LorawanClass
	// Confirmed downlinks
	// Class B - Ping slot period
	// FPort
	// ABP Personalization
	// ABP - Device address
	// ABP - NetwSKey
	// ABP - AppSKey
}

func NewLorawanConfig(devEUI, appEUI, appKey, class string) LorawanConfig {
	return LorawanConfig{
		DevEUI: strings.ToUpper(devEUI),
		AppEUI: strings.ToUpper(appEUI),
		AppKey: strings.ToUpper(appKey),
		Class:  LorawanClassFromString(class),
	}
}

func (c LorawanConfig) Matches(otherc LorawanConfig) bool {
	return c == otherc
}

func (c LorawanConfig) CheckParameters() error {
	if !utils.IsValidHex(c.DevEUI, 8*16) {
		return errors.New("Error - DevEUI must be composed of 16 hex characters")
	}
	if !utils.IsValidHex(c.AppEUI, 8*16) {
		return errors.New("Error - AppEUI must be composed of 16 hex characters")
	}
	if !utils.IsValidHex(c.AppKey, 8*32) {
		return errors.New("Error - AppKey must have 32 hex characters")
	}
	if c.Class == LorawanClassUnknown {
		return errors.New("Error - Class must be blank, A, B, or C")
	}
	return nil
}

type DeviceConfig struct {
	OCDeviceInfo
	LorawanConfig
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
		d.OwnerString(),
		d.Class)
}

// EncodeDescription generates the description for the lorawan app server
func (d DeviceConfig) EncodeDescription() string {
	return fmt.Sprintf("%s - %s", d.Name, d.OwnerString())
}

/* Functions to compare against app server app node response */

// func (d DeviceConfig) matchesAppNodeRespDevEUI(appNode *pb.GetNodeResponse) bool {
// 	return strings.ToUpper(d.DevEUI) == strings.ToUpper(appNode.DevEUI)
// }

// func (d DeviceConfig) matchesAppNodeRespAppEUI(appNode *pb.GetNodeResponse) bool {
// 	return strings.ToUpper(d.AppEUI) == strings.ToUpper(appNode.AppEUI)
// }

// func (d DeviceConfig) matchesAppNodeRespAppKey(appNode *pb.GetNodeResponse) bool {
// 	return strings.ToUpper(d.AppKey) == strings.ToUpper(appNode.AppKey)
// }

// func (d DeviceConfig) matchesAppNodeRespClass(appNode *pb.GetNodeResponse) bool {
// 	appNodeClass := "A"
// 	if appNode.IsClassC {
// 		appNodeClass = "C"
// 	}
// 	return d.Class.String() == appNodeClass
// }

// func (d DeviceConfig) matchesAppNodeRespName(appNode *pb.GetNodeResponse) bool {
// 	return d.ID == appNode.Name
// }

// func (d DeviceConfig) matchesAppNodeRespDesc(appNode *pb.GetNodeResponse) bool {
// 	return d.GetDescription() == appNode.Description
// }

// func (d DeviceConfig) matchesAppNodeRespCore(appNode *pb.GetNodeResponse) bool {
// 	return d.matchesAppNodeRespDevEUI(appNode) &&
// 		d.matchesAppNodeRespAppEUI(appNode) &&
// 		d.matchesAppNodeRespAppKey(appNode) &&
// 		d.matchesAppNodeRespClass(appNode) &&
// 		d.matchesAppNodeRespName(appNode)
// }

// func (d DeviceConfig) matchesAppNodeResp(appNode *pb.GetNodeResponse) bool {
// 	return d.matchesAppNodeRespCore(appNode) && d.matchesAppNodeRespDesc(appNode)
// }
