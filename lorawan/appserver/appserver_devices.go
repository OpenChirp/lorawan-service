package appserver

import (
	"container/list"
	"errors"
	"fmt"

	pb "github.com/brocaar/lora-app-server/api"
	. "github.com/openchirp/lorawan-service/lorawan/deviceconfig"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

var ErrDevEUIConflict = errors.New("DevEUI conflicts with another device")
var ErrDevEUINotFound = errors.New("DevEUI not found")

const DevModName = "DeviceRegistrations"
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

func (a *AppServer) DeviceList() ([]DeviceConfig, error) {
	logitem := a.log.WithField("module", DevModName)
	logitem.Debug("Getting remote device list")

	req := &pb.ListDeviceByApplicationIDRequest{
		ApplicationID: a.appid,
		Limit:         requestLimit,
		Offset:        0,
	}
	resp, err := a.Device.ListByApplicationID(context.Background(), req)
	if err != nil {
		return nil, err
	}

	devProfLookup := a.devProfilesGetReverseCache()

	configs := make([]DeviceConfig, resp.GetTotalCount())

	for i, dev := range resp.GetResult() {
		req := &pb.GetDeviceKeysRequest{
			DevEUI: dev.DevEUI,
		}
		keys, err := a.Device.GetKeys(context.Background(), req)
		if err != nil {
			return nil, errors.New(fmt.Sprintf("Failed to fetch keys for device with DevEUI=%s: %v", dev.DevEUI, err))
		}

		configs[i].OCDeviceInfo.ID = dev.Name
		configs[i].LorawanConfig = devProfLookup[dev.DeviceProfileID]
		configs[i].LorawanConfig.SetDevEUI(dev.DevEUI)
		// configs[i].LorawanConfig.SetAppEUI("")
		configs[i].LorawanConfig.SetAppKey(keys.DeviceKeys.AppKey)

		// Consistency check the real device profile ID and our (would-be) assigned ID
		if dev.GetDeviceProfileID() != a.devProfileFindRef(configs[i].LorawanConfig) {
			return nil, errors.New(fmt.Sprintf("Remote device found with uncached device profile: DevEUI=%s", dev.GetDevEUI()))
		}
	}

	return configs, nil
}

// could conflict
func (a *AppServer) DeviceRegister(config DeviceConfig) error {
	logitem := a.log.WithField("module", DevModName)
	logitem.Debugf("Registering device config %v", config)

	profid, err := a.devProfileAcquireRef(config.LorawanConfig)
	if err != nil {
		return err
	}
	req := &pb.CreateDeviceRequest{
		ApplicationID:   a.appid,
		Name:            config.ID,
		Description:     config.EncodeDescription(),
		DevEUI:          config.DevEUI,
		DeviceProfileID: profid,
	}
	_, err = a.Device.Create(context.Background(), req)
	if err != nil {
		a.devProfileReleaseRef(config.LorawanConfig)
		if grpc.Code(err) == codes.AlreadyExists {
			return ErrDevEUIConflict
		}
		return err
	}

	keyreq := &pb.CreateDeviceKeysRequest{
		DevEUI: config.DevEUI,
		DeviceKeys: &pb.DeviceKeys{
			AppKey: config.AppKey,
		},
	}
	_, err = a.Device.CreateKeys(context.Background(), keyreq)
	if err != nil {
		a.DeviceDeregister(config)
	}
	return err
}

func (a *AppServer) DeviceUpdate(config DeviceConfig) {
}

// DeviceDeregister removes a device and any dependent device profiles
// from the remote app server
// Possible errors can stem from the device not being registered on the
// remote app server OR from device profiles being out of sync (should be fatal)
func (a *AppServer) DeviceDeregister(config DeviceConfig) error {
	logitem := a.log.WithField("module", DevModName)
	logitem.Debugf("Deregistering device config %v", config)

	/* Use saved DevEUI */
	deveui := config.DevEUI

	/* Remove Device from Remote */
	req := &pb.DeleteDeviceRequest{
		DevEUI: deveui,
	}
	_, err := a.Device.Delete(context.Background(), req)
	if err != nil {
		if grpc.Code(err) == codes.NotFound {
			// Don't try to release the dev profile
			return ErrDevEUINotFound
		}
		return err
	}

	/* Remove Referenced to Device Profile */
	if err := a.devProfileReleaseRef(config.LorawanConfig); err != nil {
		return err
	}
	return nil
}
