package appserver

import (
	"errors"
	"fmt"
	"log"
	"strings"

	pb "github.com/brocaar/lora-app-server/api"
	. "github.com/openchirp/lorawan-service/lorawan/deviceconfig"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

const DevProfModName = "DeviceProfiles"

// ErrDeviceProfileStillInUse indicates that the device profile being deleted
// is still in use on the remote server
var ErrDeviceProfileStillInUse = errors.New("Device profile still in use on remote")

var defaultDeviceProfileSettings = deviceProfileSettings{
	supportsJoin:      true,
	supportsClassB:    false,
	supportsClassC:    false,
	macVersion:        "1.0.0",
	regParamsRevision: "A",
}

type deviceProfileCache struct {
	// deviceProfileSettings --> deviceProfileMeta
	profs map[deviceProfileSettings]deviceProfileMeta
}

func (dpc *deviceProfileCache) String() string {
	var str strings.Builder
	for s, m := range dpc.profs {
		str.WriteString(fmt.Sprintf("%v : %s : %d\n", s, m.ID, m.referenceCount))
	}
	return str.String()
}

type deviceProfileMeta struct {
	ID             string
	referenceCount int
}

type deviceProfileSettings struct {
	supportsJoin      bool
	supportsClassB    bool
	supportsClassC    bool
	macVersion        string
	regParamsRevision string
}

func (d deviceProfileSettings) String() string {
	var str strings.Builder
	str.WriteString("ClassA")
	if d.supportsClassB {
		str.WriteString("B")
	}
	if d.supportsClassC {
		str.WriteString("C")
	}
	if d.supportsJoin {
		str.WriteString("-JoinSupport")
	}
	mac := d.macVersion
	if mac == "" {
		mac = "ServerDefault"
	}
	reg := d.regParamsRevision
	if reg == "" {
		reg = "ServerDefault"
	}
	str.WriteString("-MAC" + mac)
	str.WriteString("-Region" + reg)
	return str.String()
}

func deviceProfileSettingsFromLorawanConfig(c LorawanConfig) deviceProfileSettings {
	s := defaultDeviceProfileSettings
	switch c.Class {
	case LorawanClassB:
		s.supportsClassB = true
	case LorawanClassC:
		s.supportsClassC = true
	}
	return s
}

func lorawanConfigFromDeviceProfileSettings(s deviceProfileSettings) LorawanConfig {
	c := LorawanConfig{
		Class: LorawanClassA,
	}

	if s.supportsClassB {
		c.Class = LorawanClassB
	} else if s.supportsClassC {
		c.Class = LorawanClassC
	}

	return c
}

func pbDeviceProfileToSettings(dp *pb.DeviceProfile) deviceProfileSettings {
	return deviceProfileSettings{
		supportsJoin:      dp.SupportsJoin,
		supportsClassB:    dp.SupportsClassB,
		supportsClassC:    dp.SupportsClassC,
		macVersion:        dp.MacVersion,
		regParamsRevision: dp.RegParamsRevision,
	}
}

func deviceProfileSettingsToPb(s deviceProfileSettings) *pb.DeviceProfile {
	return &pb.DeviceProfile{
		MacVersion:        s.macVersion,
		RegParamsRevision: s.regParamsRevision,
		// RfRegion: "EU868",
		// RfRegion: "US902",
		SupportsJoin:   s.supportsJoin,
		SupportsClassB: s.supportsClassB,
		SupportsClassC: s.supportsClassC,
	}
}

func (a *AppServer) devProfileClearAll() {
	a.devprof.profs = make(map[deviceProfileSettings]deviceProfileMeta)
}

func (a *AppServer) devProfileLoadAll() error {
	req := &pb.ListDeviceProfileRequest{
		ApplicationID:  a.appid,
		OrganizationID: a.orgid,
		Limit:          requestLimit,
		Offset:         0,
	}
	profiles, err := a.DeviceProfile.List(context.Background(), req)
	if err != nil {
		return errors.New(fmt.Sprintf("Failed to get list of device profiles: %v", err))
	}

	// fmt.Println("-- Device Profiles Start --")
	for _, p := range profiles.GetResult() {
		req := &pb.GetDeviceProfileRequest{DeviceProfileID: p.DeviceProfileID}
		profile, err := a.DeviceProfile.Get(context.Background(), req)
		if err != nil {
			return errors.New(fmt.Sprintf("Failed to get specific device profile: %v", err))
		}

		s := pbDeviceProfileToSettings(profile.GetDeviceProfile())
		m := deviceProfileMeta{
			ID: p.DeviceProfileID,
		}

		// Check for conflicting device profiles
		// Our system only cares about a certain subset of possible device
		// profile options. If there are multiple existing profiles who share
		// the same subset of options we are interested in, this is considered
		// a collision.
		if _, ok := a.devprof.profs[s]; ok {
			// This error would indicate that this profile or the one previously
			// read in are in conflict -- recall that just means that our simple
			// set of parameters match two different device profiles
			return errors.New(fmt.Sprintf("Conflicting device profiles found: ID=%s", profile.GetDeviceProfile().DeviceProfileID))
		}
		a.devprof.profs[s] = m

		// fmt.Printf("%+v\n", *p)
		// fmt.Println(profile)
		// fmt.Println("")
	}
	// fmt.Println("-- Device Profiles Done --")
	return nil
}

func (a *AppServer) devProfileCreate(s deviceProfileSettings) (deviceProfileMeta, error) {
	a.log.WithField("Module", DevProfModName).Debug("Creating profile ", s)
	req := &pb.CreateDeviceProfileRequest{
		Name:            s.String(),
		OrganizationID:  a.orgid,
		NetworkServerID: a.netwkid,
		DeviceProfile:   deviceProfileSettingsToPb(s),
	}
	m := deviceProfileMeta{}
	resp, err := a.DeviceProfile.Create(context.Background(), req)
	if err != nil {
		return m, errors.New(fmt.Sprintf("Failed to create device profile: %v", err))
	}
	m.ID = resp.DeviceProfileID
	return m, nil
}

func (a *AppServer) devProfileDelete(m deviceProfileMeta) error {
	a.log.WithField("Module", DevProfModName).Debug("Deleting profile ", m)
	req := &pb.DeleteDeviceProfileRequest{DeviceProfileID: m.ID}
	if _, err := a.DeviceProfile.Delete(context.Background(), req); err != nil {
		if grpc.Code(err) == codes.FailedPrecondition {
			return ErrDeviceProfileStillInUse
		}
		return errors.New(fmt.Sprintf("Failed to delete device profile: %v", err))
	}
	return nil
}

// devProfilePrune removes device profiles from cache with 0 references
// and removes any remote device profiles that are not referenced from the local
// cache.
// This may fail if associated remote devices are still referencing these device
// profiles.
func (a *AppServer) devProfilePrune() error {
	logitem := a.log.WithField("Module", DevProfModName)

	logitem.Debug("Pruning cache and remote device profiles")

	// remove cached unreferenced device profiles
	for s, m := range a.devprof.profs {
		if m.referenceCount < 0 {
			return errors.New(fmt.Sprintf("Negative reference count on device profile \"%v\"", s))
		}
		if m.referenceCount == 0 {
			logitem.Debug("Removing unreferenced cached profile", s)
			delete(a.devprof.profs, s)
		}
	}

	// delete extra remote device profiles
	req := &pb.ListDeviceProfileRequest{
		ApplicationID:  a.appid,
		OrganizationID: a.orgid,
		Limit:          requestLimit,
		Offset:         0,
	}
	profiles, err := a.DeviceProfile.List(context.Background(), req)
	if err != nil {
		return errors.New(fmt.Sprintf("Failed to get list of device profiles: %v", err))
	}

	for _, p := range profiles.GetResult() {
		req := &pb.GetDeviceProfileRequest{DeviceProfileID: p.DeviceProfileID}
		profile, err := a.DeviceProfile.Get(context.Background(), req)
		if err != nil {
			return errors.New(fmt.Sprintf("Failed to get specific device profile: %v", err))
		}

		s := pbDeviceProfileToSettings(profile.GetDeviceProfile())
		if m, ok := a.devprof.profs[s]; !ok || m.ID != profile.GetDeviceProfile().DeviceProfileID {
			logitem.Debug("Removing unreferenced remote device profile ", s)
			m := deviceProfileMeta{ID: profile.GetDeviceProfile().DeviceProfileID}
			if err := a.devProfileDelete(m); err != nil {
				return err
			}
		}
	}

	return nil
}

// devProfileAcquireRef creates a device profile if needed, increases its
// reference count, and returns the device profile ID
func (a *AppServer) devProfileAcquireRef(c LorawanConfig) (string, error) {
	s := deviceProfileSettingsFromLorawanConfig(c)
	m, ok := a.devprof.profs[s]
	if !ok {
		var err error
		if m, err = a.devProfileCreate(s); err != nil {
			return "", err
		}
	}
	m.referenceCount++
	a.devprof.profs[s] = m
	return m.ID, nil
}

// devProfileReleaseRef decreases the reference count for
func (a *AppServer) devProfileReleaseRef(c LorawanConfig) error {
	s := deviceProfileSettingsFromLorawanConfig(c)
	m, ok := a.devprof.profs[s]
	if !ok {
		return errors.New(fmt.Sprintf("Released uncached device profile %v", s))
	}

	m.referenceCount--

	if m.referenceCount < 0 {
		return errors.New(fmt.Sprintf("Released unreferenced device profile %v", s))
	}
	if m.referenceCount == 0 {
		delete(a.devprof.profs, s)
		return a.devProfileDelete(m)
	}
	a.devprof.profs[s] = m
	return nil
}

// devProfileFindRef searched the local cache for an associated device
// profile and returns it's ID. If it currently does not currently exist,
// we return "".
// This function is primarily for consistency checking on startup.
func (a *AppServer) devProfileFindRef(c LorawanConfig) string {
	s := deviceProfileSettingsFromLorawanConfig(c)
	m, ok := a.devprof.profs[s]
	id := ""
	if ok {
		id = m.ID
	}
	return id
}

// devProfilesGetReverseCache returns a one-off map from device profile IDs
// to LorawanConfigs.
// This function is primarily for synchronization on startup.
func (a AppServer) devProfilesGetReverseCache() map[string]LorawanConfig {
	cm := make(map[string]LorawanConfig)
	for s, m := range a.devprof.profs {
		cm[m.ID] = lorawanConfigFromDeviceProfileSettings(s)
	}
	return cm
}

func (a *AppServer) GetDeviceProfiles() {

	req := &pb.ListDeviceProfileRequest{
		ApplicationID:  a.appid,
		OrganizationID: a.orgid,
		Limit:          requestLimit,
		Offset:         0,
	}
	profiles, err := a.DeviceProfile.List(context.Background(), req)
	if err != nil {
		log.Fatalf("Failed to get list of users: %v", err)
	}

	for _, p := range profiles.GetResult() {
		fmt.Printf("%+v\n", *p)
		req := &pb.GetDeviceProfileRequest{DeviceProfileID: p.DeviceProfileID}
		profile, err := a.DeviceProfile.Get(context.Background(), req)
		if err != nil {
			log.Fatalf("Failed to get list of users: %v", err)
		}
		fmt.Println(profile)
		fmt.Println("")
	}
	fmt.Println("-- Device Profiles Done --")
}

func (a *AppServer) TestDrive() {
	cs := [...]LorawanConfig{
		LorawanConfig{
			Class: LorawanClassA,
		},
		LorawanConfig{
			Class: LorawanClassB,
		},
		LorawanConfig{
			Class: LorawanClassC,
		},
	}

	for _, c := range cs {
		id, err := a.devProfileAcquireRef(c)
		if err != nil {
			fmt.Println(err)
		}
		fmt.Println("Acquired ID", id)
	}

	a.devProfileClearAll()
	if err := a.devProfileLoadAll(); err != nil {
		fmt.Println(err)
	}

	for _, c := range cs {
		id, err := a.devProfileAcquireRef(c)
		if err != nil {
			fmt.Println(err)
		}
		fmt.Println("Acquired ID", id)
	}

	if err := a.devProfilePrune(); err != nil {
		fmt.Println(err)
	}

	// id, err := a.devProfileAcquireRef(cs[1])
	// if err != nil {
	// 	fmt.Println(err)
	// }
	// fmt.Println("Acquired ID", id)

	if err := a.devProfileReleaseRef(cs[1]); err != nil {
		fmt.Println(err)
	}

}
