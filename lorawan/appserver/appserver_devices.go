package appserver

import (
	. "github.com/openchirp/lorawan-service/lorawan/deviceconfig"
)

// DeviceSync ensures that the remote server reflects the
func (a *AppServer) DeviceSync(configs []DeviceConfig) {
	// needUpdate := list.New()
	// needAdd := list.New()
	// needRemoved := list.New()

	/* Setup - Index all devices on remote server. Mark duplicates for removal. */

	/* Match (and sort) configs with remote */
	/* Update configs that have changed */
	/* Prune remote devices that do not exist anymore */
	/* Add new configs that do not exist on remote server */
}

func (a *AppServer) DeviceList() []DeviceConfig {
	return nil
}

func (a *AppServer) DeviceAdd(config DeviceConfig) {
}

func (a *AppServer) DeviceUpdate(config DeviceConfig) {
}

func (a *AppServer) DeviceRemove(config DeviceConfig) {
}
