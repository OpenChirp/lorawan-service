package main

import (
	CRAND "crypto/rand"
	"flag"
	"fmt"
	"math/big"
	"os"
	"os/signal"

	"log"

	"strconv"

	"github.com/openchirp/framework"
	"github.com/openchirp/framework/rest"
)

const (
	defaultDeviceDescription = "Added by LoRaWAN Openchirp Service"
)

const (
	defaultframeworkhost = "http://localhost"
)

/* Options to be filled in by arguments */
var hostUri string
var user string
var pass string
var serviceId string

// genRandID generates a random id
func genRandID() string {
	r, err := CRAND.Int(CRAND.Reader, new(big.Int).SetInt64(100000))
	if err != nil {
		log.Fatal("Couldn't generate a random number for MQTT client ID")
	}
	return r.String()
}

func d(service *framework.Service, host rest.Host, appServerAppID int64, id string) {

}

/* Setup argument flags and help prompt */
func init() {
	flag.StringVar(&hostUri, "host", defaultframeworkhost, "Sets the framework host's URI")
	flag.StringVar(&user, "user", "", "Sets the framework username")
	flag.StringVar(&pass, "pass", "", "Sets the framework password")
	flag.StringVar(&serviceId, "serviceid", "", "Sets the Service ID")
}

func main() {
	/* Parse Arguments */
	flag.Parse()

	fmt.Println("# Starting up")
	fmt.Println("Host URI: ", hostUri)
	fmt.Println(user)
	fmt.Println(pass)
	fmt.Println(serviceId)

	host := rest.NewHost(hostUri)
	host.Login(user, pass)
	service, err := framework.StartService(host, serviceId)
	if err != nil {
		log.Fatalf("Failed to start service: %v\n", err)
	}
	properties := service.GetProperties()

	appServerTarget, ok := properties["AppServerTarget"]
	if !ok {
		log.Fatalf("Failed to detect LoRa App Server Target URI\n")
	}
	appServerAppID, err := strconv.ParseInt(service.GetProperty("AppServerApplicationID"), 10, 64)
	if err != nil {
		log.Fatalf("Failed to parse the AppServerApplicationID: %v\n", err)
	}
	a := NewAppServer(appServerTarget)
	fmt.Println("Properties: ", service.GetProperty("AppServerUser"), service.GetProperty("AppServerPass"))
	a.Login(service.GetProperty("AppServerUser"), service.GetProperty("AppServerPass"))
	a.Connect()
	defer a.Disconnect()

	devices := make(map[string]DeviceConfig)

	/* In order to bring up this service without missing any device configuration
	 * changes, we must carefully consider the startup order. The following
	 * comments will explain the the necessary startup order and what queuing
	 * must happen during each step:
	 */

	/* The first order of business is to fetch the current registered devices
	 * on the lora-app-server and import them as devices and their respective
	 * service config.
	 */
	appNodes, err := a.ListNodes(appServerAppID)
	if err != nil {
		log.Fatalf("Failed to fetch list of nodes from lora app server: %v\n", err)
	}
	for _, n := range appNodes {
		devices[n.DevEUI] = DeviceConfig{
			DevEUI: n.DevEUI,
			AppEUI: n.AppEUI,
			AppKey: n.AppKey,
		}
	}
	fmt.Println(devices)

	/* Next, we will subscribe to the OpenChirp service news topic and queue
	 * updates that we receive.
	 */
	updates, err := service.StartDeviceUpdates()
	if err != nil {
		log.Fatalf("Failed to start device updates channel: %v\n", err)
	}

	/* We will then fetch the static configuration from the OpenChirp
	 * framework server and resolve discrepancies with the config from the
	 * lora-app-server.
	 */
	devConfig, err := service.FetchDeviceConfigs()
	if err != nil {
		log.Fatalf("Failed to fetch initial device configs from framework server: %v\n", err)
	}
	// Add all static device configs to a map
	deviceConfigs := make(map[string]DeviceConfig)
	for _, d := range devConfig {
		// Map all key/value pairs
		config := make(map[string]string)
		for _, c := range d.Config {
			config[c.Key] = c.Value
		}
		DevEUI, ok := config["DevEUI"]
		if !ok {
			log.Printf("Device %d did not specify a DevEUI - Skipping\n", d.Id)
			continue
		}
		AppEUI, ok := config["AppEUI"]
		if !ok {
			log.Printf("Device %d did not specify a AppEUI - Skipping\n", d.Id)
			continue
		}
		AppKey, ok := config["AppKey"]
		if !ok {
			log.Printf("Device %d did not specify a AppKey - Skipping\n", d.Id)
			continue
		}
		deviceConfigs[DevEUI] = DeviceConfig{
			Id:     d.Id,
			DevEUI: DevEUI,
			AppEUI: AppEUI,
			AppKey: AppKey,
		}
	}

	// 1. Delete devices that exist on lora app server and not in framework
	//    and delete devices' whose configuration do not match
	for _, d := range devices {
		foundDevice, ok := deviceConfigs[d.DevEUI]
		if ok {
			// Check the current configuration
			if d.AppEUI == foundDevice.AppEUI && d.AppKey == foundDevice.AppKey {
				// config is good - remove from deviceConfigs map and update Id
				d.Id = foundDevice.Id
				devices[d.DevEUI] = d
				delete(deviceConfigs, d.DevEUI)
				log.Printf("Config for device %s with DevEUI %s was up-to-date\n", d.Id, d.DevEUI)
				continue
			}
			log.Printf("Config for device %s with DevEUI %s was modified\n", d.Id, d.DevEUI)
		}
		// remove from lora app server
		err = a.DeleteNode(d.DevEUI)
		if err != nil {
			log.Printf("Failed to remove DevEUI %s from app server: %v\n", d.DevEUI, err)
		}
		log.Printf("Deleted device with DevEUI %s\n", d.DevEUI)
	}
	// 2. Add remaining devices to the app server
	for _, d := range deviceConfigs {
		err = a.CreateNode(
			appServerAppID,
			d.DevEUI,
			d.AppEUI,
			d.AppKey,
			defaultDeviceDescription+" - "+d.Id,
		)
		if err != nil {
			log.Printf("Failed to create device %s with DevEUI %s on app server: %v\n", d.Id, d.DevEUI, err)
			continue
		}
		devices[d.DevEUI] = d
		delete(deviceConfigs, d.DevEUI)
		log.Printf("Added device %s with DevEUI %s to lora-app-server\n", d.Id, d.DevEUI)
	}

	// 3. Connect all MQTT topics
	for _, d := range devices {
		devInfo, err := host.RequestDeviceInfo(d.Id)
		if err != nil {
			log.Printf("Failed to fetch device info from the framework server")
			continue
		}
		fmt.Println("DevInfo: ", devInfo)
		topicRawtx := devInfo.Pubsub.Topic + "/transducer/rawtx"
		topicTx := "application/" + fmt.Sprint(appServerAppID) + "/node/" + d.DevEUI + "/tx"
		topicRx := "application/" + fmt.Sprint(appServerAppID) + "/node/" + d.DevEUI + "/rx"
		topicRawrx := devInfo.Pubsub.Topic + "/transducer/rawrx"
		err = service.Subscribe(topicRawtx, func(service *framework.Service, topic string, payload []byte) {
			err := service.Publish(
				topicTx,
				DownlinkMessage(d.DevEUI, payload),
			)
			if err != nil {
				log.Printf("Failed to publish to app server tx for DevEUI %s: %v\n", d.DevEUI, err)
			}
		})
		if err != nil {
			log.Printf("Failed to subscribe to device rawtx")
			continue
		}
		log.Printf("%s --> %s\n", topicRawtx, topicTx)

		err = service.Subscribe(topicRx, func(service *framework.Service, topic string, payload []byte) {
			err := service.Publish(
				topicRawrx,
				UplinkMessageDecode(payload),
			)
			if err != nil {
				log.Printf("Failed to publish to framework for DevEUI %s: %v\n", d.DevEUI, err)
			}
		})
		if err != nil {
			log.Printf("Failed to subscribe to device rawtx")
			continue
		}
		log.Printf("%s --> %s\n", topicRx, topicRawrx)
		log.Printf("Linked data streams for device %s with DevEUI %s\n", d.Id, d.DevEUI)
	}

	/* Finally, we start processing updates from the OpenChirp service news
	 * topic and resolving discrepancies with the lora-app-server.
	 */

	/* Wait for SIGINT */
	signals := make(chan os.Signal)
	signal.Notify(signals, os.Interrupt)

	for {
		select {
		case update := <-updates:
			m := Config2Map(update.Config)
			switch update.Type {
			case framework.DeviceUpdateTypeRem:
				// remove
				var DevEUI string

				// HACK, since we are not given DevEUI
				for _, d := range devices {
					if d.Id == update.Id {
						DevEUI = d.DevEUI
						break
					}
				}

				// HACK, we need to unsubscribe

				// DevEUI, ok := m["DevEUI"]
				// if !ok {
				// 	log.Printf("Failed to remove device %s - did not specify DevEUI\n", update.Id)
				// 	continue
				// }
				err := a.DeleteNode(DevEUI)
				if err != nil {
					log.Printf("Failed to delete device %s with DevEUI %s on lora app server: %v\n",
						update.Id,
						DevEUI,
						err)
					continue
				}
				log.Printf("Removed device %s with DevEUI %s\n", update.Id, DevEUI)
			case framework.DeviceUpdateTypeUpd:
				// remove
				DevEUI, ok := m["DevEUI"]
				if !ok {
					log.Printf("Failed to remove device %s - did not specify DevEUI\n", update.Id)
					continue
				}
				err := a.DeleteNode(DevEUI)
				if err != nil {
					log.Printf("Failed to delete device %s with DevEUI %s on lora app server: %v\n",
						update.Id,
						DevEUI,
						err)
					continue
				}
				log.Printf("Removed device %s with DevEUI %s\n", update.Id, DevEUI)
				fallthrough
			case framework.DeviceUpdateTypeAdd:
				DevEui, ok := m["DevEUI"]
				if !ok {
					log.Printf("Failed to register device %s - did not specify DevEUI\n", update.Id)
					continue
				}
				AppEUI, ok := m["AppEUI"]
				if !ok {
					log.Printf("Failed to register device %s - did not specify AppEUI\n", update.Id)
					continue
				}
				AppKey, ok := m["AppKey"]
				if !ok {
					log.Printf("Failed to register device %s - did not specify AppKey\n", update.Id)
					continue
				}
				dev := DeviceConfig{
					Id:     update.Id,
					DevEUI: DevEui,
					AppEUI: AppEUI,
					AppKey: AppKey,
				}
				devices[dev.DevEUI] = dev
				err := a.CreateNode(appServerAppID, dev.DevEUI, dev.AppEUI, dev.AppKey, defaultDeviceDescription+" - "+dev.Id)
				if err != nil {
					log.Printf("Failed to create DevEUI %s on lora app server: %v\n", dev.DevEUI, err)
				}

				devInfo, err := host.RequestDeviceInfo(dev.Id)
				if err != nil {
					log.Printf("Failed to fetch device info from the framework server")
					continue
				}
				topicRawtx := devInfo.Pubsub.Topic + "/rawtx"
				topicTx := "application/" + fmt.Sprint(appServerAppID) + "/node/" + dev.DevEUI + "/tx"
				topicRx := "application/" + fmt.Sprint(appServerAppID) + "/node/" + dev.DevEUI + "/rx"
				topicRawrx := devInfo.Pubsub.Topic + "/rawrx"
				err = service.Subscribe(topicRawtx, func(service *framework.Service, topic string, payload []byte) {
					err := service.Publish(
						topicTx,
						DownlinkMessage(dev.DevEUI, payload),
					)
					if err != nil {
						log.Printf("Failed to publish to app server tx for DevEUI %s: %v\n", dev.DevEUI, err)
					}
				})
				if err != nil {
					log.Printf("Failed to subscribe to device rawtx")
					continue
				}
				log.Printf("%s --> %s\n", topicRawtx, topicTx)

				err = service.Subscribe(topicRx, func(service *framework.Service, topic string, payload []byte) {
					err := service.Publish(
						topicRawrx,
						UplinkMessageDecode(payload),
					)
					if err != nil {
						log.Printf("Failed to publish to framework for DevEUI %s: %v\n", dev.DevEUI, err)
					}
				})
				if err != nil {
					log.Printf("Failed to subscribe to device rawtx")
					continue
				}
				log.Printf("%s --> %s\n", topicRx, topicRawrx)
				log.Printf("Linked data streams for device %s with DevEUI %s\n", dev.Id, dev.DevEUI)

				log.Printf("Added device %s with DevEUI %s\n", update.Id, dev.DevEUI)
			}
		case <-signals:
			goto shutdown
		}
	}

shutdown:
	log.Println("Shutting down")
}
