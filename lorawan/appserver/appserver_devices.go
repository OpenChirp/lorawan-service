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

func decodeOCDeviceInfo(name, description string) OCDeviceInfo {
	var i OCDeviceInfo
	i.ID = name
	i.DecodeDescription(description)
	return i
}

// DeviceSync ensures that the remote server reflects our current set of configs
func (a *AppServer) DeviceRegistrationSync(configs []DeviceConfig) ([]error, error) {
	logitem := a.log.WithField("module", DevModName)
	logitem.Debug("Syncing device configs")

	errs := make([]error, len(configs))

	type update struct {
		oldconfig      DeviceConfig
		newconfigindex int
	}

	needUpdated := list.New()
	needRemoved := list.New()

	// a.deveuis = make(map[string]string)
	a.devProfileClearAll()
	if err := a.devProfileLoadAll(); err != nil {
		return nil, err
	}

	// create a map from OC-ID --> DeviceConfig-Index (OC IDs are unique)
	localConfigMap := make(map[string]int)
	for i, c := range configs {
		localConfigMap[c.ID] = i
	}

	remoteDevs, err := a.DeviceList()
	if err != nil {
		return nil, err
	}

	/* Setup - Index all devices on remote server. Mark duplicates for removal. */

	/* Match (and sort) configs with remote */
	// need to call devProfileAcquireRef
	for _, d := range remoteDevs {
		fmt.Println(d)

		// Run profile acquire now, since we ultimately call
		// DeviceDeregister or DeviceUpdate.
		// These both assume it was already  acquired.
		a.devProfileAcquireRef(d.LorawanConfig)

		index, ok := localConfigMap[d.ID]
		if !ok {
			// Mark for removal
			needRemoved.PushBack(d)
			continue
		}
		delete(localConfigMap, d.ID)

		ld := configs[index]

		// Check if it needs to updated
		var notmatch bool
		notmatch = notmatch || !d.LorawanConfig.Matches(ld.LorawanConfig)
		notmatch = notmatch || d.ID != ld.ID
		notmatch = notmatch || d.EncodeDescription() != ld.EncodeDescription()
		if notmatch {
			// mark for update
			needUpdated.PushBack(update{d, index})
			// needUpdated.PushBack(index)
			continue
		}

		// must be a clean match
	}

	/* Prune remote devices that do not exist anymore */
	for d := needRemoved.Front(); d != nil; d = d.Next() {
		if err := a.DeviceDeregister(d.Value.(DeviceConfig)); err != nil {
			return nil, err
		}
	}

	/* Update configs that have changed -- Can conflict */
	for d := needUpdated.Front(); d != nil; d = d.Next() {
		upd := d.Value.(update)
		localdIndex := upd.newconfigindex
		oldconfig := upd.oldconfig
		newconfig := configs[localdIndex]
		errs[localdIndex] = a.DeviceUpdate(oldconfig, newconfig)
	}

	/* Add new configs that do not exist on remote server -- Can conflict */
	for _, localdIndex := range localConfigMap {
		locald := configs[localdIndex]
		errs[localdIndex] = a.DeviceRegister(locald)
	}

	/* Prune extraneous device profiles -- Already cleared extraneous devices */
	if err := a.devProfilePrune(); err != nil {
		return nil, err
	}

	return errs, nil
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

		configs[i].OCDeviceInfo = decodeOCDeviceInfo(dev.Name, dev.Description)
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
	logitem = logitem.WithField("ocid", config.ID)
	logitem.Debugf("Registering device config %v", config)

	if err := config.CheckParameters(); err != nil {
		logitem.Warnf("Parameter check failed: %v", err)
		return err
	}

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

// DeviceUpdate changes the
// could conflict
func (a *AppServer) DeviceUpdate(oldconfig, newconfig DeviceConfig) error {
	logitem := a.log.WithField("module", DevModName)
	logitem = logitem.WithField("ocid", oldconfig.ID)
	logitem.Debugf("Updating device config %v --> %v", oldconfig, newconfig)

	olddeveui := oldconfig.DevEUI

	/* Make sure new parameters are valid -- Deregister if invalid */
	if err := newconfig.CheckParameters(); err != nil {
		logitem.Warnf("Encountered a device update with invalid newconfig: %v", err)
		if e := a.DeviceDeregister(oldconfig); e != nil {
			logitem.Warnf("Failed to Deregister device with invalid newconfig: %v", e)
		}
		return err // original error
	}

	/* Check is DevEUI changed */
	if olddeveui != newconfig.DevEUI {
		logitem.Debug("DevEUI needs updating")
		if err := a.DeviceDeregister(oldconfig); err != nil {
			return err
		}
		return a.DeviceRegister(newconfig)
	}

	/* Get DeviceConfig from remote */
	getreq := &pb.GetDeviceRequest{
		DevEUI: olddeveui,
	}
	resp, err := a.Device.Get(context.Background(), getreq)
	if err != nil {
		if grpc.Code(err) == codes.NotFound {
			return ErrDevEUINotFound
		}
		return err
	}

	deveui := newconfig.DevEUI

	/* Check is Device Profile has changed -- Could be comparing to "" if does not yet exist */
	devProfChanged := resp.DeviceProfileID != a.devProfileFindRef(newconfig.LorawanConfig)

	/* Check if Name or Description changed */
	nameDescChanged := (resp.Name != newconfig.ID) || (resp.Description != newconfig.EncodeDescription())

	/* If either Device Profile or Name/Description changed form an update request */
	/* It turns out that you cannot just update one without the other. Both
	device profile and name/description must be updated together. */

	updateReq := &pb.UpdateDeviceRequest{
		DevEUI:          deveui,
		Name:            resp.Name,
		Description:     resp.Description,
		DeviceProfileID: resp.DeviceProfileID,
	}

	if devProfChanged {
		logitem.Debug("Device Profile needs updating")
		profid, err := a.devProfileAcquireRef(newconfig.LorawanConfig)
		if err != nil {
			return err
		}
		updateReq.DeviceProfileID = profid
	}

	if nameDescChanged {
		logitem.Debug("Name and/or Description needs updating")
		updateReq.Name = newconfig.ID
		updateReq.Description = newconfig.EncodeDescription()
	}

	_, err = a.Device.Update(context.Background(), updateReq)
	if err != nil {
		if grpc.Code(err) == codes.NotFound {
			return ErrDevEUINotFound
		}
		return err
	}

	if devProfChanged {
		err := a.devProfileReleaseRef(oldconfig.LorawanConfig)
		if err != nil {
			return err
		}
	}

	/* Get Device Keys from Remote */
	keysreq := &pb.GetDeviceKeysRequest{
		DevEUI: deveui,
	}
	keys, err := a.Device.GetKeys(context.Background(), keysreq)
	if err != nil {
		return errors.New(fmt.Sprintf("Failed to fetch keys for device with DevEUI=%s: %v", deveui, err))
	}

	/* Check is AppKey changed */
	if keys.DeviceKeys.AppKey != newconfig.AppKey {
		logitem.Debug("AppKey needs updating")
		keyChangeReq := &pb.UpdateDeviceKeysRequest{
			DevEUI: deveui,
			DeviceKeys: &pb.DeviceKeys{
				AppKey: newconfig.AppKey,
			},
		}

		_, err := a.Device.UpdateKeys(context.Background(), keyChangeReq)
		if err != nil {
			if grpc.Code(err) == codes.NotFound {
				return ErrDevEUINotFound
			}
			return err
		}
	}

	return nil
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
