package lorawan

import (
	"fmt"
	"strings"

	"github.com/openchirp/framework"
	"github.com/openchirp/framework/pubsub"
	"github.com/sirupsen/logrus"
)

const (
	LorawanClassA LorawanClass = iota
	LorawanClassB
	LorawanClassC
	LorawanClassUnknown
)

// StatusMsgAlreadyRegistered is followed by an OC Device ID
var StatusMsgAlreadyRegistered = "DevEUI already registered to"

type LorawanClass int

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
	case "A":
		return LorawanClassC
	case "B":
		return LorawanClassC
	case "C":
		return LorawanClassC
	default:
		return LorawanClassUnknown
	}
}

type DeviceConfig struct {
	// OC Device ID
	ID string
	// OC MQTT Topic
	Topic string
	// OC Device Nname
	Name string
	// OC Device Owner
	Owner string

	DevEUI string
	AppEUI string
	AppKey string
	Class  LorawanClass
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
		d.Owner)
}

func (d DeviceConfig) CheckParameters() string {
	// TODO: Implement parameter checking
	// Should return message indicating problem if there is one
	return ""
}

func (d DeviceConfig) GetDescription() string {
	return fmt.Sprintf("%s - %s", d.Name, d.Owner)
}

type DeviceConfigStatusUpdate func(config DeviceConfig, str string)

type Manager struct {
	bridge       pubsub.Bridge // A is OC, B is LoRa App Server
	appID        int64
	app          AppServer
	configStatus DeviceConfigStatusUpdate
	log          *logrus.Logger
}

func NewManager(
	bridge pubsub.Bridge,
	app AppServer,
	appID int64,
	configStatus DeviceConfigStatusUpdate,
	log *logrus.Logger) *Manager {
	l := new(Manager)
	l.bridge = bridge
	l.appID = appID
	l.app = app
	l.configStatus = configStatus
	l.log = log
	return l
}

func (l *Manager) configStatusOk(config DeviceConfig) {
	l.configStatus(config, fmt.Sprintf("%s %s", StatusMsgOk, config.DevEUI))
}
func (l *Manager) configStatusFailed(config DeviceConfig, msg string) {
	l.configStatus(config, msg)
}
func (l *Manager) configStatusConflict(conflicConfig DeviceConfig, chosenID string) {
	l.configStatus(conflicConfig, fmt.Sprintf("%s %s", StatusMsgAlreadyRegistered, chosenID))
}

func (l *Manager) addBridge(config DeviceConfig) error {
	// TODO: Implement
	return nil
}
func (l *Manager) remBridge(config DeviceConfig) error {
	// TODO: Implement
	return nil
}

// Sync synchronizes the LoRa App server to the given device updates.
// The sync function returns
func (l *Manager) Sync(configs []DeviceConfig) error {

	/* Build map from DevEUI --> DeviceConfig from configs */
	l.log.Debugln("Organizing initial configs")
	devIDToConfigs := make(map[string][]DeviceConfig)
	for _, c := range configs {
		configArray := devIDToConfigs[c.DevEUI]
		if configArray == nil {
			configArray = make([]DeviceConfig, 0, 1)
		}
		devIDToConfigs[c.DevEUI] = append(configArray, c)
	}

	/* In order to bring up this service without missing any device configuration
	 * changes, we must carefully consider the startup order. The following
	 * comments will explain the the necessary startup order and what queuing
	 * must happen during each step:
	 */

	/* The first order of business is to fetch the current registered devices
	 * on the lora-app-server and import them as devices and their respective
	 * service config.
	 */

	/* The following sets up/verifies all devices register on the LoRa App Server,
	   while also denying device configs that conflict the app server's nodes
	*/
	l.log.Debugln("Fetching lora app server config")
	appNodes, err := l.app.ListNodes(l.appID)
	if err != nil {
		return fmt.Errorf("Failed to fetch list of nodes from lora app server: %v\n", err)
	}
	for _, appNode := range appNodes {
		// compare this app server node to initial config
		if devConfigs, ok := devIDToConfigs[appNode.DevEUI]; ok {
			// handle all device configs with this DevEUI listed
			var chosenIndex int = 0
			for i, c := range devConfigs {
				if c.ID == appNode.Name {
					chosenIndex = i
				}
			}

			// run with chosenIndex

			// The Differing Field Actions:
			// DevEUI      - Must Match By Definition of Scope
			// AppEUI      - Delete and Add
			// AppKey      - Delete and Add
			// Class       - Delete and Add
			// Name        - Delete and Add (must have been under different ownership)
			// Description - Simple Update (only thing that allows simple update)
			chosenConfig := devConfigs[chosenIndex]
			appNodeClass := "A"
			if appNode.IsClassC {
				appNodeClass = "C"
			}
			chosenConfigIsClassC := false
			if chosenConfig.Class == LorawanClassC {
				chosenConfigIsClassC = true
			}

			if errs := chosenConfig.CheckParameters(); len(errs) > 0 {
				l.log.Infof("Device ID \"%s\" failed parameter check, so it will be deleted and not re-added: %s", errs)
				err = l.app.DeleteNode(chosenConfig.DevEUI)
				if err != nil {
					// RUNTIME ERROR ON INIT - NOT GOOD
					return fmt.Errorf("Failed delete node from app server with DevEUI \"%s\": %v", chosenConfig.DevEUI, err)
				}
				l.configStatusFailed(chosenConfig, errs)
			} else {
				if chosenConfig.AppEUI != appNode.AppEUI ||
					chosenConfig.AppKey != appNode.AppKey ||
					chosenConfig.Name != appNodes.Name ||
					chosenConfig.Class.String() != appNodeClass {

					// Needs a full refresh - session key will be wiped
					err = l.app.DeleteNode(chosenConfig.DevEUI)
					if err != nil {
						// RUNTIME ERROR ON INIT - NOT GOOD
						return fmt.Errorf("Failed delete node from app server with DevEUI \"%s\": %v", chosenConfig.DevEUI, err)
					}

					err = l.app.CreateNodeWithClass(
						l.appID,
						chosenConfig.DevEUI,
						chosenConfig.AppEUI,
						chosenConfig.AppKey,
						chosenConfig.ID,
						chosenConfig.GetDescription(),
						chosenConfigIsClassC,
					)
					if err != nil {
						// RUNTIME ERROR ON INIT - NOT GOOD
						return fmt.Errorf("Failed create node on app server with DevEUI \"%s\" and ID \"\": ", chosenConfig.DevEUI, chosenConfig.ID, err)
					}
				} else if chosenConfig.GetDescription() != appNode.Description {
					// Simply update the description on the app server: %v", chosen
					err = l.app.UpdateNodeDescription(chosenConfig.DevEUI, chosenConfig.GetDescription())
					if err != nil {
						return fmt.Errorf("Failed update description of node on app server with DevEUI \"%s\": %v", chosenConfig.DevEUI, err)
					}
				}

				err := l.addBridge(chosenConfig)
				if err != nil {
					// RUNTIME ERROR ON INIT - NOT GOOD
					return fmt.Errorf("Failed to link data streams for node with ID \"%s\": %v", chosenConfig.ID, err)
				}
				l.configStatusOk(chosenConfig)
			}

			// send bad status to not chosenIndex
			for i, c := range devConfigs {
				if i != chosenIndex {
					l.configStatusConflict(c, chosenConfig.ID)
				}
			}

			delete(devIDToConfigs, appNode.DevEUI)
		} else {
			// remove from app server
			err := l.app.DeleteNode(appNode.DevEUI)
			if err != nil {
				// RUNTIME ERROR ON INIT - NOT GOOD
				return fmt.Errorf("Failied to delete node with DevEUI \"%s\" from app server: %v", appNode.DevEUI, err)
			}
		}
	}

	/* Add all remaining device configs and handle conflicts(randomly) */
	for _, devConfigs := range devIDToConfigs {
		// FIXME: We Need A Way To Establish Which Device's Config Should Win

		var chosenIndex int = 0

		// run with chosenIndex

		// The Differing Field Actions:
		// DevEUI      - Must Match By Definition of Scope
		// AppEUI      - Delete and Add
		// AppKey      - Delete and Add
		// Class       - Delete and Add
		// Name        - Delete and Add (must have been under different ownership)
		// Description - Simple Update (only thing that allows simple update)
		chosenConfig := devConfigs[chosenIndex]
		appNodeClass := "A"
		if appNode.IsClassC {
			appNodeClass = "C"
		}
		chosenConfigIsClassC := false
		if chosenConfig.Class == LorawanClassC {
			chosenConfigIsClassC = true
		}

		err = l.app.CreateNodeWithClass(
			l.appID,
			chosenConfig.DevEUI,
			chosenConfig.AppEUI,
			chosenConfig.AppKey,
			chosenConfig.ID,
			chosenConfig.GetDescription(),
			chosenConfigIsClassC,
		)
		if err != nil {
			// RUNTIME ERROR ON INIT - NOT GOOD
			return fmt.Errorf("Failed create node on app server with DevEUI \"%s\" and ID \"\": ", chosenConfig.DevEUI, chosenConfig.ID, err)
		}

		err := l.addBridge(chosenConfig)
		if err != nil {
			// RUNTIME ERROR ON INIT - NOT GOOD
			return fmt.Errorf("Failed to link data streams for node with ID \"%s\": %v", chosenConfig.ID, err)
		}
		l.configStatusOk(chosenConfig)

		// send bad status to not chosenIndex
		for i, c := range devConfigs {
			if i != chosenIndex {
				l.configStatusConflict(c, chosenConfig.ID)
			}
		}

		delete(devIDToConfigs, appNode.DevEUI)
	}

	// /* Next, we will subscribe to the OpenChirp service news topic and queue
	//  * updates that we receive.
	//  */
	// l.log.Debugln("Start listening for framework events")
	// s.events, err = s.service.StartDeviceUpdates()
	// if err != nil {
	// 	s.err.Fatalf("Failed to start framework events channel: %v\n", err)
	// }

	// /* We will then fetch the static configuration from the OpenChirp
	//  * framework server and resolve discrepancies with the config from the
	//  * lora-app-server.
	//  */
	// l.log.Debugln("Fetching device configurations from framework")
	// devConfig, err := s.service.FetchDeviceConfigs()
	// if err != nil {
	// 	s.err.Fatalf("Failed to fetch initial device configs from framework server: %v\n", err)
	// }
	// // Add all static device configs to a map
	// deviceConfigs := make(map[string]DeviceConfig)
	// for _, d := range devConfig {
	// 	// Map all key/value pairs
	// 	config := Config2Map(d.Config)
	// 	DevEUI, ok := config["DevEUI"]
	// 	if !ok {
	// 		s.err.Printf("Device %s did not specify a DevEUI - Skipping\n", d.Id)
	// 		continue
	// 	}
	// 	AppEUI, ok := config["AppEUI"]
	// 	if !ok {
	// 		s.err.Printf("Device %s did not specify a AppEUI - Skipping\n", d.Id)
	// 		continue
	// 	}
	// 	AppKey, ok := config["AppKey"]
	// 	if !ok {
	// 		s.err.Printf("Device %s did not specify a AppKey - Skipping\n", d.Id)
	// 		continue
	// 	}
	// 	deviceConfigs[DevEUI] = DeviceConfig{
	// 		ID:     d.Id,
	// 		DevEUI: DevEUI,
	// 		AppEUI: AppEUI,
	// 		AppKey: AppKey,
	// 	}
	// }

	// // 1. Delete devices that exist on lora app server and not in framework,
	// //    delete devices whose configuration do not match, and fill in missing
	// //    device fields(ID and Topic)
	// for _, d := range s.devices {
	// 	foundDevice, ok := deviceConfigs[d.DevEUI]
	// 	if ok {
	// 		// Check the current configuration
	// 		if d.AppEUI == foundDevice.AppEUI && d.AppKey == foundDevice.AppKey {
	// 			// config is good - remove from deviceConfigs map and update Id
	// 			d.ID = foundDevice.ID
	// 			// we still don't have the mqtt topic yet
	// 			s.devices[d.DevEUI] = d
	// 			delete(deviceConfigs, d.DevEUI)
	// 			s.std.Printf("Config for device %s with DevEUI %s was up-to-date\n", d.ID, d.DevEUI)
	// 			continue
	// 		}
	// 		s.std.Printf("Config for device %s with DevEUI %s was modified\n", d.ID, d.DevEUI)
	// 	}
	// 	// remove from lora app server
	// 	err = s.app.DeleteNode(d.DevEUI)
	// 	if err != nil {
	// 		s.err.Printf("Failed to remove DevEUI %s from app server: %v\n", d.DevEUI, err)
	// 	}
	// 	s.std.Printf("Deleted device with DevEUI %s\n", d.DevEUI)
	// }
	// // 2. Add remaining devices to the app server
	// for _, d := range deviceConfigs {
	// 	if err := s.PullDeviceConfig(&d); err != nil {
	// 		s.err.Printf("Failed to fetch device info from the framework server: %v\n", err)
	// 		continue
	// 	}
	// 	if err := s.CreateAppEntry(d); err != nil {
	// 		s.err.Printf("Failed to create device %s with DevEUI %s on app server: %v\n", d.ID, d.DevEUI, err)
	// 		continue
	// 	}
	// 	s.devices[d.DevEUI] = d
	// 	delete(deviceConfigs, d.DevEUI)
	// 	s.std.Printf("Added device %s with DevEUI %s to lora-app-server\n", d.ID, d.DevEUI)
	// }

	// // 3. Connect all MQTT topics
	// for _, d := range s.devices {
	// 	if err := s.PullDeviceConfig(&d); err != nil {
	// 		s.err.Println("Device Pulled: ", d)
	// 		s.err.Printf("Failed to fetch device info from the framework server: %v\n", err)
	// 		continue
	// 	}
	// 	s.err.Println("Device Pulled: ", d)
	// 	s.devices[d.DevEUI] = d
	// 	if err := s.LinkData(d); err != nil {
	// 		s.err.Printf("Failed to link device data: %v\n", err)
	// 	}
	// }
	return nil
}

func (l *Manager) Process(update framework.DeviceUpdate) error {
	return nil
}
