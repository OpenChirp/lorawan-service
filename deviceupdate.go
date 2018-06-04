package main

import (
	"fmt"
	"strings"

	"github.com/openchirp/framework"
	. "github.com/openchirp/lorawan-service/lorawan/deviceconfig"
)

type DeviceUpdateAdapter struct {
	framework.DeviceUpdate
}

func (update DeviceUpdateAdapter) GetDevEUI() string {
	return strings.ToLower(update.Config[configDevEUI])
}

func (update DeviceUpdateAdapter) GetAppEUI() string {
	return strings.ToLower(update.Config[configAppEUI])
}

func (update DeviceUpdateAdapter) GetAppKey() string {
	return strings.ToLower(update.Config[configAppKey])
}

func (update DeviceUpdateAdapter) GetClass() LorawanClass {
	classStr := update.Config[configClass]
	class := LorawanClassFromString(classStr)
	if classStr == "" {
		class = defaultLorawanClass
	}
	return class
}

func (update DeviceUpdateAdapter) GetLorawanDeviceConfig(c *framework.ServiceClient) (DeviceConfig, error) {
	var config DeviceConfig

	config.ID = update.Id

	if c != nil {
		info, err := c.FetchDeviceInfo(update.Id)
		if err != nil {
			return config, fmt.Errorf("Deviceid \"%s\" was deleted before we could fetch it's config. Skipping.", update.Id)
		}
		config.Topic = info.Pubsub.Topic
		config.Name = info.Name
		config.Owner = info.Owner.Email
	}

	config.DevEUI = update.GetDevEUI()
	config.AppEUI = update.GetAppEUI()
	config.AppKey = update.GetAppKey()
	config.Class = update.GetClass()

	return config, nil
}
