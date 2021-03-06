package main

import (
	"fmt"
	"strings"

	"github.com/openchirp/framework"
	. "github.com/openchirp/lorawan-service/lorawan/deviceconfig"
)

const defaultLorawanClass = LorawanClassA

type DeviceUpdateAdapter struct {
	framework.DeviceUpdate
}

func (update DeviceUpdateAdapter) GetDevEUI() string {
	return strings.ToUpper(update.Config[configDevEUI])
}

func (update DeviceUpdateAdapter) GetAppKey() string {
	return strings.ToUpper(update.Config[configAppKey])
}

func (update DeviceUpdateAdapter) GetClass() LorawanClass {
	classStr := update.Config[configClass]
	class := LorawanClassFromString(classStr)
	if classStr == "" {
		class = defaultLorawanClass
	}
	return class
}

func (update DeviceUpdateAdapter) GetDeviceConfig(c *framework.ServiceClient) (DeviceConfig, error) {
	var config DeviceConfig

	config.ID = update.Id

	switch update.Type {
	case framework.DeviceUpdateTypeAdd, framework.DeviceUpdateTypeUpd:
		info, err := c.FetchDeviceInfo(update.Id)
		if err != nil {
			return config, fmt.Errorf("Deviceid \"%s\" was deleted before we could fetch it's config. Skipping.", update.Id)
		}
		config.Topic = info.Pubsub.Topic
		config.Name = info.Name
		config.OwnerName = info.Owner.Name
		config.OwnerEmail = info.Owner.Email
	}

	config.SetDevEUI(update.GetDevEUI())
	config.SetAppKey(update.GetAppKey())
	config.Class = update.GetClass()

	return config, nil
}
