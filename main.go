package main

import (
	CRAND "crypto/rand"
	"flag"
	"fmt"
	"math/big"
	"os"
	"os/signal"

	"log"

	"time"

	"github.com/openchirp/framework"
	"github.com/openchirp/framework/rest"
)

const (
	appServerJWTRefreshTime = time.Minute * time.Duration(60)
)

const (
	defaultframeworkserver = "http://localhost"
)

/* Options to be filled in by arguments */
var frameworkURI string
var user string
var pass string
var serviceID string

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
	flag.StringVar(&frameworkURI, "framework", defaultframeworkserver, "Sets the framework host's URI")
	flag.StringVar(&user, "user", "", "Sets the framework username")
	flag.StringVar(&pass, "pass", "", "Sets the framework password")
	flag.StringVar(&serviceID, "serviceid", "", "Sets the Service ID")
}

func main() {
	/* Parse Arguments */
	flag.Parse()

	fmt.Println("# Starting up")
	fmt.Println("Host URI: ", frameworkURI)
	fmt.Println(user)
	fmt.Println(pass)
	fmt.Println(serviceID)

	s := StartLorawanService(frameworkURI, serviceID, user, pass)
	defer s.Stop()

	s.SyncSequence()
	/* In order to bring up this service without missing any device configuration
	 * changes, we must carefully consider the startup order. The following
	 * comments will explain the the necessary startup order and what queuing
	 * must happen during each step:
	 */

	/* Finally, we start processing updates from the OpenChirp service news
	 * topic and resolving discrepancies with the lora-app-server.
	 */

	/* Wait for SIGINT */
	signals := make(chan os.Signal)
	signal.Notify(signals, os.Interrupt)

	for {
		select {
		case update := <-s.events:
			m := Config2Map(update.Config)
			switch update.Type {
			case framework.DeviceUpdateTypeRem:
				// remove
				var d DeviceConfig

				// HACK, since we are not given DevEUI
				for _, dev := range s.devices {
					if dev.ID == update.Id {
						d = dev
						delete(s.devices, d.DevEUI)
						break
					}
				}

				if len(d.DevEUI) == 0 {
					s.err.Printf("Framework server sent me a remove of an invalid device ID\n")
					continue
				}

				// HACK, we need to unsubscribe

				if err := s.DeleteAppEntry(d); err != nil {
					s.err.Printf("Failed to delete DevEUI %s from lora app server: %v\n", d.DevEUI, err)
				}

				if err := s.UnlinkData(d); err != nil {
					s.err.Printf("Failed to unlink data for DevEUI %s: %v\n", d.DevEUI, err)
				}

				s.std.Printf("Removed device %s with DevEUI %s\n", d.ID, d.DevEUI)

			case framework.DeviceUpdateTypeUpd:
				// remove
				var d DeviceConfig

				// HACK, since we are not given DevEUI
				for _, dev := range s.devices {
					if dev.ID == update.Id {
						d = dev
						delete(s.devices, d.DevEUI)
						break
					}
				}

				if len(d.DevEUI) == 0 {
					s.err.Printf("Framework server sent me a remove of an invalid device ID\n")
					continue
				}

				// HACK, we need to unsubscribe

				if err := s.DeleteAppEntry(d); err != nil {
					s.err.Printf("Failed to delete DevEUI %s from lora app server: %v\n", d.DevEUI, err)
				}

				if err := s.UnlinkData(d); err != nil {
					s.err.Printf("Failed to unlink data for DevEUI %s: %v\n", d.DevEUI, err)
				}

				s.std.Printf("Removed device %s with DevEUI %s\n", d.ID, d.DevEUI)
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
					ID:     update.Id,
					DevEUI: DevEui,
					AppEUI: AppEUI,
					AppKey: AppKey,
				}

				if err := s.PullDeviceConfig(&dev); err != nil {
					s.UnlinkData(dev)
					s.DeleteAppEntry(dev)
					s.err.Printf("Failed to fetch device info from the framework server: %v\n", err)
					continue
				}
				s.devices[dev.DevEUI] = dev

				// Create device entry on lora app server
				if err := s.CreateAppEntry(dev); err != nil {
					s.err.Printf("Failed to create DevEUI %s on lora app server: %v\n", dev.DevEUI, err)
					continue
				}

				if err := s.LinkData(dev); err != nil {
					s.DeleteAppEntry(dev)
					s.err.Printf("Failed to link device data: %v\n", err)
					continue
				}

				log.Printf("Added device %s with DevEUI %s\n", update.Id, dev.DevEUI)
			}
		case <-time.After(appServerJWTRefreshTime):
			s.std.Println("Reconnecting to app server")
			err := s.ReLoginAppServer()
			if err != nil {
				s.err.Fatalf("Failed to relogin the app server: %v\n", err)
			}
		case <-signals:
			goto shutdown
		}
	}

shutdown:
	log.Println("Shutting down")
}
