package lorawan

import (
	"encoding/base64"
	"fmt"

	"github.com/openchirp/framework/pubsub"
	"github.com/sirupsen/logrus"
)

// StatusMsgAlreadyRegistered is followed by an OC Device ID
var StatusMsgAlreadyRegistered = "DevEUI already registered to"
var StatusMsgOk = "Successfully registered DevEUI"

type DeviceConfigStatusUpdate func(config DeviceConfig, str string)

type Manager struct {
	bridge        *pubsub.Bridge // A is OC, B is LoRa App Server
	appID         int64
	app           *AppServer
	configStatus  DeviceConfigStatusUpdate
	devIDToDevEUI map[string]string
	log           *logrus.Logger
}

func NewManager(
	bridge *pubsub.Bridge,
	app *AppServer,
	appID int64,
	configStatus DeviceConfigStatusUpdate,
	log *logrus.Logger) *Manager {
	l := new(Manager)
	l.bridge = bridge
	l.appID = appID
	l.app = app
	l.configStatus = configStatus
	l.devIDToDevEUI = make(map[string]string)
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
		l.remove(deviceConfig)
		l.configStatusFailed(deviceConfig, "Failed - Data stream bridge denied links")
		return
	}
	l.devIDToDevEUI[deviceConfig.ID] = deviceConfig.DevEUI
	l.configStatusOk(deviceConfig)
}

// updateDescriptionAndAddOnly simply updates the description on the app server
// and add the device
func (l *Manager) updateDescriptionAndAddOnly(deviceConfig DeviceConfig) {
	logitem := l.log.WithField("deviceid", deviceConfig.ID).WithField("deveui", deviceConfig.DevEUI)

	logitem.Debug("Updating Node Description on App Server")

	deviceConfigIsClassC := false
	if deviceConfig.Class == LorawanClassC {
		deviceConfigIsClassC = true
	}

	// Simply update the description on the app server: %v", chosen
	err := l.app.UpdateNode(
		l.appID,
		deviceConfig.DevEUI,
		deviceConfig.AppEUI,
		deviceConfig.AppKey,
		deviceConfig.ID,
		deviceConfig.GetDescription(),
		deviceConfigIsClassC,
	)
	if err != nil {
		logitem.Errorf("Failed update description of node on app server: %v", err)
		l.configStatusFailed(deviceConfig, "Failed - App server denied registration")
		return
	}

	logitem.Debug("Adding data stream bridge")
	err = l.addBridge(deviceConfig)
	if err != nil {
		logitem.Errorf("Failed to link data stream: %v", err)
		l.remove(deviceConfig)
		l.configStatusFailed(deviceConfig, "Failed - Data stream bridge denied links")
		return
	}
	l.devIDToDevEUI[deviceConfig.ID] = deviceConfig.DevEUI
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

func (l *Manager) remove(deviceConfig DeviceConfig) {
	logitem := l.log.WithField("deviceid", deviceConfig.ID)

	if devEUI, ok := l.devIDToDevEUI[deviceConfig.ID]; ok {
		logitem.Debug("Deleting node from app server")
		err := l.app.DeleteNode(devEUI)
		if err != nil {
			logitem.Errorf("Failed to delete node from app server: %v", err)
		}
	} else {
		logitem.Errorf("Attempted to delete unknown device ID")
	}

	err := l.remBridge(deviceConfig)
	if err != nil {
		logitem.Errorf("Failed remove links: %v", err)
	}

	delete(l.devIDToDevEUI, deviceConfig.ID)
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
				// If Parameters Are Bad
				l.log.Infof("Device ID \"%s\" failed parameter check, so it will be deleted and not re-added: %s", errs)
				err = l.app.DeleteNode(chosenConfig.DevEUI)
				if err != nil {
					// RUNTIME ERROR ON INIT - NOT GOOD
					return fmt.Errorf("Failed delete node from app server with DevEUI \"%s\": %v", chosenConfig.DevEUI, err)
				}
				l.configStatusFailed(chosenConfig, errs)
			} else {
				// If Parameters Are Good
				if chosenConfig.AppEUI != appNode.AppEUI ||
					chosenConfig.AppKey != appNode.AppKey ||
					chosenConfig.ID != appNode.Name ||
					chosenConfig.Class.String() != appNodeClass {
					// Needs a full refresh and link - session key will be wiped
					l.log.WithField("deviceid", chosenConfig.ID).Debug("App server disagrees with core parameters")
					l.refreshAndAddOnly(chosenConfig)
				} else if chosenConfig.GetDescription() != appNode.Description {
					// Simply update the description on the app server and link
					l.log.WithField("deviceid", chosenConfig.ID).Debug("App server disagrees with description")
					l.updateDescriptionAndAddOnly(chosenConfig)
				} else {
					l.log.WithField("deviceid", chosenConfig.ID).Debug("App server agrees with parameters")
					l.addBridge(chosenConfig)
					l.devIDToDevEUI[chosenConfig.ID] = chosenConfig.DevEUI
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

func (l *Manager) ProcessAdd(update DeviceConfig) {
	logitem := l.log.WithField("deviceid", update.ID)
	/* Tip toe until we know they have rights to commit this update to DevEUI */

	devEUI, ok := l.devIDToDevEUI[update.ID]

	if ok && (devEUI != update.DevEUI) {
		l.remove(update)
	}

	appNode, _ := l.app.GetNode(update.DevEUI)

	// If device doesn't exist - Add it
	if appNode == nil {
		logitem.Debug("Adding device")
		l.addOnly(update)
		return
	}

	// Check that this deviceid is the owner of the DevEUI
	if appNode.Name != update.ID {
		logitem.Debug("Device conflicts with another deviceid")
		l.configStatusConflict(update, appNode.Name)
		return
	}

	appNodeClass := "A"
	if appNode.IsClassC {
		appNodeClass = "C"
	}

	// Must Be An Update
	if update.AppEUI != appNode.AppEUI ||
		update.AppKey != appNode.AppKey ||
		update.ID != appNode.Name ||
		update.Class.String() != appNodeClass {

		// Needs a full refresh and link - session key will be wiped
		l.bridge.RemoveLinksAll(update.ID)
		l.refreshAndAddOnly(update)
	} else if update.GetDescription() != appNode.Description {
		// Simply update the description on the app server and link
		l.updateDescriptionAndAddOnly(update)
	} else {
		l.addBridge(update)
		l.devIDToDevEUI[update.ID] = update.DevEUI
	}

}

func (l *Manager) ProcessUpdate(update DeviceConfig) {
	logitem := l.log.WithField("deviceid", update.ID)
	/* Tip toe until we know they have rights to commit this update to DevEUI */

	devEUI, ok := l.devIDToDevEUI[update.ID]

	// If it was denied before, try to add it this time
	if !ok {
		l.ProcessAdd(update)
		return
	}

	// If they are attempting to change their DevEUI, we need to go through
	// Add process again
	if devEUI != update.DevEUI {
		l.remove(update)
		l.ProcessAdd(update)
		return
	}

	/* They must have rights to update this DevEUI */

	appNode, _ := l.app.GetNode(update.DevEUI)
	if appNode == nil {
		// This is an awkward situation, since we said we added it, but it isn't
		// on the app server. It must have errored out when we added it to the
		// app server
		// Action: Cleanup and Add
		logitem.Errorf("Received an update for a deviceid and DevEUI we added, but it is missing from app server")
		l.remove(update)
		l.addOnly(update)
		return
	}
	if appNode.Name != update.ID {
		// Another awkward situation, since our internal map said that this
		// DevEUI was owned by this deviceid and the app server says it is
		// is owned by some other deviceid(or some rando).
		// Action: Remove, Cleanup, Add
		logitem.Errorf("Received an update for a deviceid and DevEUI we added, but the app server reports a different Name/deviceid")
		l.remove(update)
		l.addOnly(update)
		return
	}

	appNodeClass := "A"
	if appNode.IsClassC {
		appNodeClass = "C"
	}

	// Must Be An Update
	if update.AppEUI != appNode.AppEUI ||
		update.AppKey != appNode.AppKey ||
		update.Class.String() != appNodeClass {

		// Needs a full refresh and link - session key will be wiped
		logitem.Debug("Changes require refreshing device on app server")
		l.remove(update)
		l.addOnly(update)
	} else if update.GetDescription() != appNode.Description {
		// Simply update the description on the app server and link
		logitem.Debug("Simply updating description")
		l.updateDescriptionAndAddOnly(update)
	}

}

func (l *Manager) ProcessRemove(update DeviceConfig) {
	// Since it looks up DevEUI from internal map, it will only remove if it
	// was granted the previously requested DevEUI upon add or update
	l.remove(update)
}
