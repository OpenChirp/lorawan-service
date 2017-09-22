package main

import (
	"fmt"
	"log"
	"strconv"

	"os"

	"github.com/openchirp/framework"
	"github.com/openchirp/framework/rest"
)

type DeviceConfig struct {
	ID     string
	Topic  string // OpenChirp MQTT Topic
	DevEUI string
	AppEUI string
	AppKey string
	// Maybe have the device owner in the future
	Name  string
	Owner string
}

func (d DeviceConfig) String() string {
	return fmt.Sprintf("ID: %s, DevEUI: %s, AppEUI: %s, AppKey: %s, Name: %s, Owner: %s",
		d.ID,
		d.DevEUI,
		d.AppEUI,
		d.AppKey,
		d.Name,
		d.Owner)
}

func (d DeviceConfig) GetDescription() string {
	return fmt.Sprintf("%s - %s - %s", d.Name, d.Owner, d.ID)
}

func Config2Map(config []rest.KeyValuePair) map[string]string {
	m := make(map[string]string)
	for _, c := range config {
		m[c.Key] = c.Value
	}
	return m
}

type LorawanService struct {
	// host      rest.Host
	serviceID string
	service   *framework.ServiceClient
	app       AppServer
	events    <-chan framework.DeviceUpdate

	appServerID int64

	std *log.Logger
	err *log.Logger

	devices map[string]DeviceConfig
}

func StartLorawanService(hosturi, serviceid, user, pass string) *LorawanService {
	var err error
	s := new(LorawanService)

	/* Setup logging */
	s.std = log.New(os.Stderr, "", log.Flags()|log.Lshortfile)
	s.err = log.New(os.Stderr, "", log.Flags()|log.Lshortfile)

	/* Setup framework REST interface */
	s.std.Println("Setting up framework REST interface")
	s.host = rest.NewHost(hosturi)
	s.host.Login(user, pass)
	s.serviceID = serviceid

	/* Start framework service  */
	s.std.Println("Starting framework service")
	s.service, err = framework.StartService(s.host, s.serviceID)
	if err != nil {
		s.err.Fatalf("Failed to start framework service: %v\n", err)
	}

	/* Start lora app server connection */
	properties := s.service.GetProperties()
	appServerTarget, ok := properties["AppServerTarget"]
	if !ok {
		s.err.Fatalf("Failed to detect LoRa App Server Target URI\n")
	}
	s.appServerID, err = strconv.ParseInt(s.service.GetProperty("AppServerApplicationID"), 10, 64)
	if err != nil {
		s.err.Fatalf("Failed to parse the AppServerApplicationID: %v\n", err)
	}
	s.app = NewAppServer(appServerTarget)
	appUser := s.service.GetProperty("AppServerUser")
	appPass := s.service.GetProperty("AppServerPass")
	s.std.Printf("Connecting to lora app server as %s:%s\n", appUser, appPass)
	err = s.app.Login(appUser, appPass)
	if err != nil {
		s.err.Fatalf("Failed Login to App Server: %v\n", err)
	}
	err = s.app.Connect()
	if err != nil {
		s.err.Fatalf("Failed Connect to App Server: %v\n", err)
	}

	/* Setup other runtime variable */
	s.devices = make(map[string]DeviceConfig)

	return s
}

func (s *LorawanService) Stop() {
	s.service.StopService()
	s.app.Disconnect()
}

func (s *LorawanService) PullDeviceConfig(d *DeviceConfig) error {
	devInfo, err := s.host.RequestDeviceInfo(d.ID)
	if err != nil {
		return err
	}
	d.Name = devInfo.Name
	d.Topic = devInfo.Pubsub.Topic
	d.Owner = devInfo.Owner.Email
	return nil
}

func (s *LorawanService) CreateAppEntry(d DeviceConfig) error {
	desc := d.GetDescription()
	return s.app.CreateNode(s.appServerID, d.DevEUI, d.AppEUI, d.AppKey, desc)
}

func (s *LorawanService) DeleteAppEntry(d DeviceConfig) error {
	s.std.Printf("Deleting device %s from lora app server with DevEUI %s\n", d.ID, d.DevEUI)
	return s.app.DeleteNode(d.DevEUI)
}

// func (s *LorawanService) LinkData(d DeviceConfig) error {
// 	topicRawtx := d.Topic + "/transducer/rawtx"
// 	topicTx := "application/" + fmt.Sprint(s.appServerID) + "/node/" + d.DevEUI + "/tx"
// 	topicRx := "application/" + fmt.Sprint(s.appServerID) + "/node/" + d.DevEUI + "/rx"
// 	topicRawrx := d.Topic + "/transducer/rawrx"

// 	s.std.Printf("Subscribing to %s\n", topicRawtx)
// 	err := s.service.Subscribe(topicRawtx, func(service *framework.Service, topic string, payload []byte) {
// 		// we need to decode base64, so that the following marshaller can
// 		// re-encode it as base64
// 		data, err := base64.StdEncoding.DecodeString(string(payload))
// 		if err != nil {
// 			s.err.Printf("Failed to decode base64 for DevEUI %s: %v\n", d.DevEUI, err)
// 			return
// 		}
// 		err = service.Publish(
// 			topicTx,
// 			DownlinkMessage(d.DevEUI, data),
// 		)
// 		if err != nil {
// 			s.err.Printf("Failed to publish to app server tx for DevEUI %s: %v\n", d.DevEUI, err)
// 		}
// 	})
// 	if err != nil {
// 		return err
// 	}
// 	s.std.Printf("%s --> %s\n", topicRawtx, topicTx)

// 	s.std.Printf("Subscribing to %s\n", topicRx)
// 	err = s.service.Subscribe(topicRx, func(service *framework.Service, topic string, payload []byte) {
// 		err := service.Publish(
// 			topicRawrx,
// 			UplinkMessageDecode(payload),
// 		)
// 		if err != nil {
// 			s.err.Printf("Failed to publish to framework for DevEUI %s: %v\n", d.DevEUI, err)
// 		}
// 	})
// 	if err != nil {
// 		// Simply try to unsubscribe to previous topic and return error
// 		s.service.Unsubscribe(topicRawtx)
// 		return err
// 	}
// 	s.std.Printf("%s --> %s\n", topicRx, topicRawrx)
// 	s.std.Printf("Linked data streams for device %s with DevEUI %s\n", d.ID, d.DevEUI)
// 	return nil
// }

func (s *LorawanService) UnlinkData(d DeviceConfig) error {
	var returnErr error
	topicRawtx := d.Topic + "/transducer/rawtx"
	topicRx := "application/" + fmt.Sprint(s.appServerID) + "/node/" + d.DevEUI + "/rx"

	// we will return first error seen

	s.std.Printf("Unsubscribing from %s\n", topicRawtx)
	err := s.service.Unsubscribe(topicRawtx)
	if err != nil {
		returnErr = err
	}
	s.std.Printf("Unsubscribing from %s\n", topicRx)
	err = s.service.Unsubscribe(topicRx)
	if err != nil && returnErr == nil {
		returnErr = err
	}
	return returnErr

}

func (s *LorawanService) SyncSequence() {
	/* In order to bring up this service without missing any device configuration
	 * changes, we must carefully consider the startup order. The following
	 * comments will explain the the necessary startup order and what queuing
	 * must happen during each step:
	 */

	/* The first order of business is to fetch the current registered devices
	 * on the lora-app-server and import them as devices and their respective
	 * service config.
	 */
	s.std.Println("Fetching lora app server config")
	appNodes, err := s.app.ListNodes(s.appServerID)
	if err != nil {
		s.err.Fatalf("Failed to fetch list of nodes from lora app server: %v\n", err)
	}
	for _, n := range appNodes {
		s.devices[n.DevEUI] = DeviceConfig{
			DevEUI: n.DevEUI,
			AppEUI: n.AppEUI,
			AppKey: n.AppKey,
		}
	}

	/* Next, we will subscribe to the OpenChirp service news topic and queue
	 * updates that we receive.
	 */
	s.std.Println("Start listening for framework events")
	s.events, err = s.service.StartDeviceUpdates()
	if err != nil {
		s.err.Fatalf("Failed to start framework events channel: %v\n", err)
	}

	/* We will then fetch the static configuration from the OpenChirp
	 * framework server and resolve discrepancies with the config from the
	 * lora-app-server.
	 */
	s.std.Println("Fetching device configurations from framework")
	devConfig, err := s.service.FetchDeviceConfigs()
	if err != nil {
		s.err.Fatalf("Failed to fetch initial device configs from framework server: %v\n", err)
	}
	// Add all static device configs to a map
	deviceConfigs := make(map[string]DeviceConfig)
	for _, d := range devConfig {
		// Map all key/value pairs
		config := Config2Map(d.Config)
		DevEUI, ok := config["DevEUI"]
		if !ok {
			s.err.Printf("Device %s did not specify a DevEUI - Skipping\n", d.Id)
			continue
		}
		AppEUI, ok := config["AppEUI"]
		if !ok {
			s.err.Printf("Device %s did not specify a AppEUI - Skipping\n", d.Id)
			continue
		}
		AppKey, ok := config["AppKey"]
		if !ok {
			s.err.Printf("Device %s did not specify a AppKey - Skipping\n", d.Id)
			continue
		}
		deviceConfigs[DevEUI] = DeviceConfig{
			ID:     d.Id,
			DevEUI: DevEUI,
			AppEUI: AppEUI,
			AppKey: AppKey,
		}
	}

	// 1. Delete devices that exist on lora app server and not in framework,
	//    delete devices whose configuration do not match, and fill in missing
	//    device fields(ID and Topic)
	for _, d := range s.devices {
		foundDevice, ok := deviceConfigs[d.DevEUI]
		if ok {
			// Check the current configuration
			if d.AppEUI == foundDevice.AppEUI && d.AppKey == foundDevice.AppKey {
				// config is good - remove from deviceConfigs map and update Id
				d.ID = foundDevice.ID
				// we still don't have the mqtt topic yet
				s.devices[d.DevEUI] = d
				delete(deviceConfigs, d.DevEUI)
				s.std.Printf("Config for device %s with DevEUI %s was up-to-date\n", d.ID, d.DevEUI)
				continue
			}
			s.std.Printf("Config for device %s with DevEUI %s was modified\n", d.ID, d.DevEUI)
		}
		// remove from lora app server
		err = s.app.DeleteNode(d.DevEUI)
		if err != nil {
			s.err.Printf("Failed to remove DevEUI %s from app server: %v\n", d.DevEUI, err)
		}
		s.std.Printf("Deleted device with DevEUI %s\n", d.DevEUI)
	}
	// 2. Add remaining devices to the app server
	for _, d := range deviceConfigs {
		if err := s.PullDeviceConfig(&d); err != nil {
			s.err.Printf("Failed to fetch device info from the framework server: %v\n", err)
			continue
		}
		if err := s.CreateAppEntry(d); err != nil {
			s.err.Printf("Failed to create device %s with DevEUI %s on app server: %v\n", d.ID, d.DevEUI, err)
			continue
		}
		s.devices[d.DevEUI] = d
		delete(deviceConfigs, d.DevEUI)
		s.std.Printf("Added device %s with DevEUI %s to lora-app-server\n", d.ID, d.DevEUI)
	}

	// 3. Connect all MQTT topics
	for _, d := range s.devices {
		if err := s.PullDeviceConfig(&d); err != nil {
			s.err.Println("Device Pulled: ", d)
			s.err.Printf("Failed to fetch device info from the framework server: %v\n", err)
			continue
		}
		s.err.Println("Device Pulled: ", d)
		s.devices[d.DevEUI] = d
		if err := s.LinkData(d); err != nil {
			s.err.Printf("Failed to link device data: %v\n", err)
		}
	}
}

func (s *LorawanService) ReLoginAppServer() error {
	return s.app.ReLogin()
}
