package deviceconfig

import (
	"errors"
	"fmt"
	"strings"

	"github.com/openchirp/lorawan-service/lorawan/utils"
	"github.com/sirupsen/logrus"
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
	if ocd.OwnerEmail == "" && ocd.OwnerName == "" {
		return ""
	}
	return ocd.OwnerEmail + "(" + ocd.OwnerName + ")"
}

// EncodeDescription generates the description for the lorawan app server
func (d OCDeviceInfo) EncodeDescription() string {
	return fmt.Sprintf("%s | %s | %s", d.Name, d.OwnerEmail, d.OwnerName)
}

// LogrusFields returns the OCDeviceInfo fields as loggable Logrus Fields
func (d OCDeviceInfo) LogrusFields() logrus.Fields {
	return logrus.Fields{
		"OCID":    d.ID,
		"OCName":  d.Name,
		"OCOwner": d.OwnerString(),
		"OCTopic": d.Topic,
	}
}

func (d *OCDeviceInfo) DecodeDescription(descritption string) {
	parts := strings.SplitN(descritption, " | ", 3)
	d.Name = ""
	d.OwnerEmail = ""
	d.OwnerName = ""
	if len(parts) > 0 {
		d.Name = parts[0]
	}
	if len(parts) > 1 {
		d.OwnerEmail = parts[1]
	}
	if len(parts) > 2 {
		d.OwnerName = parts[2]
	}
}

func (ocd OCDeviceInfo) Matches(otherocd OCDeviceInfo) bool {
	return ocd == otherocd
}

type LorawanConfig struct {
	DevEUI string
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

func (c *LorawanConfig) SetDevEUI(devEUI string) {
	c.DevEUI = strings.ToUpper(devEUI)
}

func (c *LorawanConfig) SetAppKey(appKey string) {
	c.AppKey = strings.ToUpper(appKey)
}

func (c LorawanConfig) Matches(otherc LorawanConfig) bool {
	var nok bool
	nok = nok || c.DevEUI != otherc.DevEUI
	nok = nok || c.AppKey != otherc.AppKey
	nok = nok || c.Class != otherc.Class
	return !nok
}

func (c LorawanConfig) CheckParameters() error {
	if !utils.IsValidHex(c.DevEUI, 8*8) || (len(c.DevEUI) != 16) {
		return errors.New("DevEUI must be composed of 16 hex characters")
	}
	if !utils.IsValidHex(c.AppKey, 8*16) || (len(c.AppKey) != 32) {
		return errors.New("AppKey must have 32 hex characters")
	}
	if c.Class == LorawanClassUnknown {
		return errors.New("Class must be blank, A, B, or C")
	}
	return nil
}

// LogrusFields returns the LorawanConfig fields as loggable Logrus Fields
func (c LorawanConfig) LogrusFields() logrus.Fields {
	return logrus.Fields{
		"DevEUI": c.DevEUI,
		"AppKey": c.AppKey,
		"Class":  c.Class,
	}
}

type DeviceConfig struct {
	OCDeviceInfo
	LorawanConfig
}

func (d DeviceConfig) String() string {
	return fmt.Sprintf("ID: %s, DevEUI: %s, AppKey: %s, Name: %s, Owner: %s, Class: %v",
		d.ID,
		d.DevEUI,
		d.AppKey,
		d.Name,
		d.OwnerString(),
		d.Class)
}

func (d DeviceConfig) Matches(otherd DeviceConfig) bool {
	lcok := d.LorawanConfig.Matches(otherd.LorawanConfig)
	ociok := d.OCDeviceInfo.Matches(otherd.OCDeviceInfo)
	return lcok && ociok
}

// LogrusFields returns all DeviceConfig fields as loggable Logrus Fields
func (d DeviceConfig) LogrusFields() logrus.Fields {
	var fields logrus.Fields
	fields = d.OCDeviceInfo.LogrusFields()
	for k, v := range d.LorawanConfig.LogrusFields() {
		fields[k] = v
	}
	return fields
}
