package lorawan

import (
	"encoding/base64"
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
var StatusMsgOk = "Successfully registered DevEUI"

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
	if len(d.DevEUI) != 16 {
		return "Error - DevEUI must have 16 hex characters"
	}
	if len(d.AppEUI) != 16 {
		return "Error - AppEUI must have 16 hex characters"
	}
	if len(d.AppKey) != 32 {
		return "Error - AppKey must have 32 hex characters"
	}
	return ""
}

func (d DeviceConfig) GetDescription() string {
	return fmt.Sprintf("%s - %s", d.Name, d.Owner)
}

type DeviceConfigStatusUpdate func(config DeviceConfig, str string)

type Manager struct {
	bridge       *pubsub.Bridge // A is OC, B is LoRa App Server
	appID        int64
	app          AppServer
	configStatus DeviceConfigStatusUpdate
	log          *logrus.Logger
}

func NewManager(
	bridge *pubsub.Bridge,
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

func (l *Manager) addBridge(d DeviceConfig) error {
	logitem := l.log.WithField("deviceid", d.ID)

	topicRawtx := d.Topic + "/transducer/rawtx"
	topicTx := "application/" + fmt.Sprint(l.appID) + "/node/" + d.DevEUI + "/tx"
	topicRx := "application/" + fmt.Sprint(l.appID) + "/node/" + d.DevEUI + "/rx"
	topicRawrx := d.Topic + "/transducer/rawrx"

	logitem.Debug("Adding fwd tx link")
	err := l.bridge.AddFwd(
		d.ID,
		topicRawtx,
		func(pubsubb pubsub.PubSub, topica string, payload []byte) error {
			// we need to decode base64, so that the following marshaller can
			// re-encode it as base64
			data, err := base64.StdEncoding.DecodeString(string(payload))
			if err != nil {
				return err
			}

			return pubsubb.Publish(topicTx, DownlinkMessage(d.DevEUI, data))
		})
	if err != nil {
		l.bridge.RemoveLinksAll(d.ID)
		return err
	}

	logitem.Debug("Adding rev rx link")
	err = l.bridge.AddRev(
		d.ID,
		topicRx,
		func(pubsuba pubsub.PubSub, topicb string, payload []byte) error {
			return pubsuba.Publish(topicRawrx, UplinkMessageDecode(payload))
		})
	if err != nil {
		l.bridge.RemoveLinksAll(d.ID)
		return err
	}
	return nil
}
func (l *Manager) remBridge(d DeviceConfig) error {
	logitem := l.log.WithField("deviceid", d.ID)
	logitem.Debug("Removing link")
	return l.bridge.RemoveLinksAll(d.ID)
}

// addOnly simply add the device to app server and creates brigde without any
// checks of app server
func (l *Manager) addOnly(deviceConfig DeviceConfig) {
	logitem := l.log.WithField("deviceid", deviceConfig.ID).WithField("deveui", deviceConfig.DevEUI)

	deviceConfigIsClassC := false
	if deviceConfig.Class == LorawanClassC {
		deviceConfigIsClassC = true
	}

	logitem.Debug("Checking parameters")
	if errs := deviceConfig.CheckParameters(); errs != "" {
		logitem.Warnf("Failed to add device: %s", errs)
		l.configStatusFailed(deviceConfig, errs)
		return
	}

	logitem.Debug("Creating Node on App Server")
	err := l.app.CreateNodeWithClass(
		l.appID,
		deviceConfig.DevEUI,
		deviceConfig.AppEUI,
		deviceConfig.AppKey,
		deviceConfig.ID,
		deviceConfig.GetDescription(),
		deviceConfigIsClassC,
	)
	if err != nil {
		logitem.Errorf("Failed create node on app server: %v", err)
		l.configStatusFailed(deviceConfig, "Failed - App server denied registration")
		return
	}

	logitem.Debug("Adding data stream bridge")
	err = l.addBridge(deviceConfig)
	if err != nil {
		logitem.Errorf("Failed to link data stream: %v", err)
		l.configStatusFailed(deviceConfig, "Failed - Data stream bridge denied links")
		return
	}
	l.configStatusOk(deviceConfig)
}

// updateDescriptionAndAddOnly simply updates the description on the app server
// and add the device
func (l *Manager) updateDescriptionAndAddOnly(deviceConfig DeviceConfig) {
	logitem := l.log.WithField("deviceid", deviceConfig.ID).WithField("deveui", deviceConfig.DevEUI)

	logitem.Debug("Updating Node Description on App Server")
	// Simply update the description on the app server: %v", chosen
	err := l.app.UpdateNodeDescription(deviceConfig.DevEUI, deviceConfig.GetDescription())
	if err != nil {
		logitem.Errorf("Failed update description of node on app server: %v", err)
		l.configStatusFailed(deviceConfig, "Failed - App server denied registration")
		return
	}

	logitem.Debug("Adding data stream bridge")
	err = l.addBridge(deviceConfig)
	if err != nil {
		logitem.Errorf("Failed to link data stream: %v", err)
		l.configStatusFailed(deviceConfig, "Failed - Data stream bridge denied links")
		return
	}
	l.configStatusOk(deviceConfig)
}

// refreshAndAddOnly deletes the node on the app server and adds it using new config
func (l *Manager) refreshAndAddOnly(deviceConfig DeviceConfig) {
	logitem := l.log.WithField("deviceid", deviceConfig.ID).WithField("deveui", deviceConfig.DevEUI)

	logitem.Debug("Deleting node from app server")
	err := l.app.DeleteNode(deviceConfig.DevEUI)
	if err != nil {
		logitem.Errorf("Failed delete node from app server: %v", err)
		return
	}

	l.addOnly(deviceConfig)
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
			// chosenConfigIsClassC := false
			// if chosenConfig.Class == LorawanClassC {
			// 	chosenConfigIsClassC = true
			// }

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
					chosenConfig.Name != appNode.Name ||
					chosenConfig.Class.String() != appNodeClass {

					// Needs a full refresh and link - session key will be wiped
					l.refreshAndAddOnly(chosenConfig)

				} else if chosenConfig.GetDescription() != appNode.Description {
					// Simply update the description on the app server and link
					l.updateDescriptionAndAddOnly(chosenConfig)
				}
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
		l.addOnly(chosenConfig)

		// send bad status to not chosenIndex
		for i, c := range devConfigs {
			if i != chosenIndex {
				l.configStatusConflict(c, chosenConfig.ID)
			}
		}

		delete(devIDToConfigs, chosenConfig.DevEUI)
	}
	return nil
}

func (l *Manager) ProcessAdd(update DeviceConfig) error {
	l.addOnly(update)
	return nil
}

func (l *Manager) ProcessUpdate(update DeviceConfig) error {
	return l.ProcessAdd(update)
}

func (l *Manager) ProcessRemove(update DeviceConfig) error {

	return nil
}
