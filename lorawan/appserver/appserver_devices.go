package appserver

import (
	"container/list"
	"errors"
	"fmt"

	pb "github.com/brocaar/lora-app-server/api"
	. "github.com/openchirp/lorawan-service/lorawan/deviceconfig"
	"github.com/sirupsen/logrus"
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
	logitem := a.log.WithField("Module", DevModName)
	logitem.Info("Syncing device configs")

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
	logitem.Info("Indexing all devices that exist on remote app server")

	/* Match (and sort) configs with remote */
	// need to call devProfileAcquireRef
	for _, d := range remoteDevs {
		logitem := logitem.WithFields(logrus.Fields{
			"OCID":    d.ID,
			"OCName":  d.Name,
			"OCOwner": d.OwnerString(),
			"DevEUI":  d.LorawanConfig.DevEUI,
			"AppEUI":  d.LorawanConfig.AppEUI,
			"AppKey":  d.LorawanConfig.AppKey,
			"Class":   d.LorawanConfig.Class,
		})

		logitem.Info("Found device")

		// Run profile acquire now, since we ultimately call
		// DeviceDeregister or DeviceUpdate.
		// These both assume it was already  acquired.
		a.devProfileAcquireRef(d.LorawanConfig)

		index, ok := localConfigMap[d.ID]
		if !ok {
			// Mark for removal
			logitem.Debug("Marking for removal")
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
			logitem.Debug("Marking for update")
			needUpdated.PushBack(update{d, index})
			// needUpdated.PushBack(index)
			continue
		}

		// must be a clean match
		logitem.Debugf("Accepted device")
	}

	/* Prune remote devices that do not exist anymore */
	logitem.Info("Prune remote devices that do not exist anymore")
	for d := needRemoved.Front(); d != nil; d = d.Next() {
		if err := a.DeviceDeregister(d.Value.(DeviceConfig)); err != nil {
			return nil, err
		}
	}

	/* Update configs that have changed -- Can conflict */
	logitem.Info("Update configs that have changed")
	for d := needUpdated.Front(); d != nil; d = d.Next() {
		upd := d.Value.(update)
		localdIndex := upd.newconfigindex
		oldconfig := upd.oldconfig
		newconfig := configs[localdIndex]
		errs[localdIndex] = a.DeviceUpdate(oldconfig, newconfig)
	}

	/* Add new configs that do not exist on remote server -- Can conflict */
	logitem.Info("Add new configs to remove server")
	for _, localdIndex := range localConfigMap {
		locald := configs[localdIndex]
		errs[localdIndex] = a.DeviceRegister(locald)
	}

	/* Prune extraneous device profiles -- Already cleared extraneous devices */
	logitem.Info("Prune extra device profiles")
	if err := a.devProfilePrune(); err != nil {
		return nil, err
	}

	return errs, nil
}

func (a *AppServer) DeviceList() ([]DeviceConfig, error) {
	logitem := a.log.WithField("Module", DevModName)
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
	logitem := a.log.WithFields(logrus.Fields{
		"Module":  DevModName,
		"OCID":    config.ID,
		"OCName":  config.Name,
		"OCOwner": config.OwnerString(),
		"DevEUI":  config.LorawanConfig.DevEUI,
		"AppEUI":  config.LorawanConfig.AppEUI,
		"AppKey":  config.LorawanConfig.AppKey,
		"Class":   config.LorawanConfig.Class,
	})

	logitem.Debugf("Registering device config")

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
	logitem := a.log.WithFields(logrus.Fields{
		"Module": DevModName,

		"OldOCID":    oldconfig.ID,
		"OldOCName":  oldconfig.Name,
		"OldOCOwner": oldconfig.OwnerString(),
		"OldDevEUI":  oldconfig.LorawanConfig.DevEUI,
		"OldAppEUI":  oldconfig.LorawanConfig.AppEUI,
		"OldAppKey":  oldconfig.LorawanConfig.AppKey,
		"OldClass":   oldconfig.LorawanConfig.Class,

		"OCID":    newconfig.ID,
		"OCName":  newconfig.Name,
		"OCOwner": newconfig.OwnerString(),
		"DevEUI":  newconfig.LorawanConfig.DevEUI,
		"AppEUI":  newconfig.LorawanConfig.AppEUI,
		"AppKey":  newconfig.LorawanConfig.AppKey,
		"Class":   newconfig.LorawanConfig.Class,
	})

	logitem.Info("Updating device config")

	/* Sanity Check */
	if oldconfig.ID != newconfig.ID {
		logitem.Fatalf("The sky is falling! DeviceUpdate was given an old and new config with mismatched OC IDsa")
		return errors.New("DeviceUpdate was given an old and new config with mismatched OC IDs")
	}

	/* Make sure new parameters are valid -- Deregister if invalid */
	if err := newconfig.CheckParameters(); err != nil {
		logitem.Infof("Encountered a device update with invalid newconfig: %v", err)
		if e := a.DeviceDeregister(oldconfig); e != nil {
			logitem.Infof("Failed to Deregister device with invalid newconfig: %v", e)
		}
		return err // original error
	}

	olddeveui := oldconfig.DevEUI

	/* Get DeviceConfig from remote */
	getreq := &pb.GetDeviceRequest{
		DevEUI: olddeveui,
	}
	resp, err := a.Device.Get(context.Background(), getreq)
	if err != nil {
		if grpc.Code(err) == codes.NotFound {
			// Try to register a new
			logitem.Debugf("Old config not found on server, trying to register as new device")
			return a.DeviceRegister(newconfig)
			// return ErrDevEUINotFound
		}
		return err
	}

	var serverDeviceConfig DeviceConfig
	serverDeviceConfig.OCDeviceInfo = decodeOCDeviceInfo(resp.Name, resp.Description)

	/* Device exists on server, but not our own OC ID */
	if serverDeviceConfig.ID != oldconfig.ID {
		// Try to register a new
		logitem.Debugf("Old config is owned byb another device, trying to register as new device")
		return a.DeviceRegister(newconfig)
	}

	/* From this point on, we assume the server contains our own oldconfig */

	/* Check if DevEUI changed */
	if olddeveui != newconfig.DevEUI {
		logitem.Debug("DevEUI needs updating")
		if err := a.DeviceDeregister(oldconfig); err != nil {
			return err
		}
		return a.DeviceRegister(newconfig)
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
	logitem := a.log.WithFields(logrus.Fields{
		"Module":  DevModName,
		"OCID":    config.ID,
		"OCName":  config.Name,
		"OCOwner": config.OwnerString(),
		"DevEUI":  config.LorawanConfig.DevEUI,
		"AppEUI":  config.LorawanConfig.AppEUI,
		"AppKey":  config.LorawanConfig.AppKey,
		"Class":   config.LorawanConfig.Class,
	})

	logitem.Debugf("Deregistering device config")

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

func (a *AppServer) DebugDump() {
	// Temporarily change log level to info
	originalLevel := a.log.Level
	a.log.Level = logrus.InfoLevel

	logitem := a.log.WithField("Module", DevModName).WithField("Debug", "dump")

	logitem.Infof("addr = %s | user = %s | pass %s | jwt = %s", a.addr, a.user, a.pass, a.jwt)
	logitem.Infof("DeviceProfileCache:\n%s", a.devprof.String())

	a.log.Level = originalLevel
}
