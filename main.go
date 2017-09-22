package main

import (
	"os"
	"os/signal"
	"syscall"

	"time"

	"github.com/openchirp/framework"
	"github.com/openchirp/framework/pubsub"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
)

const (
	runningStatus           = true
	appServerJWTRefreshTime = time.Minute * time.Duration(60)
)

const (
	appServerMqttPersistance = false
	configDevEUI             = "DevEUI"
	configAppEUI             = "AppEUI"
	configAppKey             = "AppKey"
)

func run(ctx *cli.Context) error {

	/* Set logging level */
	log := logrus.New()
	log.SetLevel(logrus.Level(uint32(ctx.Int("log-level"))))

	log.Info("Starting Byte Translator Service ")

	/* Start framework service client */
	c, err := framework.StartServiceClientStatus(
		ctx.String("framework-server"),
		ctx.String("mqtt-server"),
		ctx.String("service-id"),
		ctx.String("service-token"),
		"Unexpected disconnect!")
	if err != nil {
		log.Error("Failed to StartServiceClient: ", err)
		return cli.NewExitError(nil, 1)
	}
	defer c.StopClient()
	log.Debug("Started service")

	/* Post service status indicating I am starting */
	err = c.SetStatus("Starting")
	if err != nil {
		log.Error("Failed to publish service status: ", err)
		return cli.NewExitError(nil, 1)
	}
	log.Debug("Published Service Status")

	/* Connect to App Server's MQTT Broker */
	if ctx.Uint("app-mqtt-qos") < 0 || 2 < ctx.Uint("app-mqtt-qos") {
		log.Fatal("App Server QoS out of valid range")
		return cli.NewExitError(nil, 1)
	}
	appMqtt, err := pubsub.NewMQTTClient(
		ctx.String("app-mqtt-server"),
		ctx.String("app-mqtt-user"),
		ctx.String("app-mqtt-pass"),
		pubsub.MQTTQoS(ctx.Uint("app-mqtt-qos")),
		appServerMqttPersistance,
	)
	if err != nil {
		log.Fatal("Failed to connect to App Server's MQTT Broker: ", err)
		return cli.NewExitError(nil, 1)
	}
	defer appMqtt.Disconnect()

	/* Launch LoRaWAN Service */
	var lorawan LorawanManager
	lorawan.bridge = pubsub.NewBridge(c, appMqtt, log)
	devIdToDevEui := make(map[string]string)

	// appServerTarget := c.GetProperty("AppServerTarget")
	// appUser := c.GetProperty("AppServerUser")
	// appPass := c.GetProperty("AppServerPass")
	// appAppId := c.GetProperty("AppServerApplicationID")

	appServerTarget := ctx.String("app-grpc-server")
	appServerUser := ctx.String("app-grpc-user")
	appServerPass := ctx.String("app-grpc-pass")
	appAppId := ctx.Int64("app-appid")

	lorawan.app = NewAppServer(appServerTarget)
	log.Debugf("Connecting to lora app server as %s:%s\n", appServerUser, appServerPass)
	if err := app.Login(appServerUser, appServerPass); err != nil {
		log.Fatalf("Failed Login to App Server: %v\n", err)
	}
	if err := app.Connect(); err != nil {
		log.Fatalf("Failed Connect to App Server: %v\n", err)
	}
	defer app.Disconnect()

	/* Start service main device updates stream */

	// start framework event stream
	log.Debug("Starting device updates stream")
	updates, err := c.StartDeviceUpdates()
	if err != nil {
		log.Error("Failed to start device updates stream: ", err)
		return cli.NewExitError(nil, 1)
	}
	defer c.StopDeviceUpdates()

	// fetch initial service configs
	log.Debug("Fetching framework initial device configs")
	configUpdates, err := c.FetchDeviceConfigsAsUpdates()
	if err != nil {
		log.Error("Failed to fetch initial device configs: ", err)
		return cli.NewExitError(nil, 1)
	}

	// fetch initial app server configs
	log.Debug("Fetching lora app server initial configs")
	appNodes, err := app.ListNodes(appAppId)
	if err != nil {
		log.Error("Failed to fetch app server initial configs: ", err)
		return cli.NewExitError(nil, 1)
	}

	for _, n := range appNodes {
		n.DevEUI
	}

	/* Shooting down DevEUIs on App Server that do not exist anymore */
	err = c.SetStatus("Shooting down orphaned DevEUIs")
	if err != nil {
		log.Fatal("Failed to publish service status: ", err)
		return cli.NewExitError(nil, 1)
	}
	log.Debug("Shooting down orphaned DevEUIs")
	initialFwConfigs, err := c.FetchDeviceConfigs()
	if err != nil {
		log.Fatal("Failed to fetch initial configuration from framework: ", err)
		return cli.NewExitError(nil, 1)
	}
	initialAppConfig, err := app.ListNodes(appAppId)
	if err != nil {
		log.Fatal("Failed to fetch initial configuration from App Server: ", err)
		err = c.SetStatus("Failed to fetch initial configuration from App Server: ", err)
		if err != nil {
			log.Fatal("Failed to publish service status: ", err)
		}
		return cli.NewExitError(nil, 1)
	}

	// TODO: Delete DevEUIs on App Server that don't exist anymore

	// We don't need anymore
	initialFwConfigs = nil
	initialAppConfig = nil

	/* Post service status indicating I started */
	err = c.SetStatus("Started")
	if err != nil {
		log.Error("Failed to publish service status: ", err)
		return cli.NewExitError(nil, 1)
	}
	log.Debug("Published Service Status")

	/* Setup signal channel */
	log.Debug("Processing device updates")
	signals := make(chan os.Signal)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)

	/* Start runtime event loop */
	for {
		select {
		case update := <-updates:
			/* If runningStatus is set, post a service status as an alive msg */
			if runningStatus {
				err = c.SetStatus("Running")
				if err != nil {
					log.Error("Failed to publish service status: ", err)
					return cli.NewExitError(nil, 1)
				}
				log.Debug("Published Service Status")
			}

			logitem := log.WithFields(logrus.Fields{
				"type":     update.Type,
				"deviceid": update.Id,
			})

			/*
			 1. Shoot down DevEUIs that don't exist anymore
			 2.
			*/

			if update.Type == framework.DeviceUpdateTypeRem ||
				update.Type == framework.DeviceUpdateTypeUpd {
				logitem.Info("Removing device")
				if DevEUI, ok := devIdToDevEui[update.Id]; ok {
					// app.DeleteNode
					bridge.RemoveLinksAll(update.Id)
					delete(devIdToDevEui, update.Id)
				} else {
					logitem.Warnf("Tried to remove without it being registered")
				}
			}

			if update.Type == framework.DeviceUpdateTypeAdd ||
				update.Type == framework.DeviceUpdateTypeUpd {
				logitem.Info("Adding device")

				// If we need to make sure the device is

				if DevEUI, ok := devIdToDevEui[update.Id]; ok {
					// app.DeleteNode
				} else {
					bridge.RemoveLinksAll(update.Id)
					devIdToDevEui[update.Id] = nil
				}
			}

		case <-time.After(appServerJWTRefreshTime):
			log.Debug("Reconnecting to app server")
			if err := app.ReLogin(); err != nil {
				log.Fatalf("Failed to relogin the app server: %v\n", err)
				err = c.SetStatus("Failed to relogin to app server: ", err)
				if err != nil {
					log.Error("Failed to publish service status: ", err)
				}
			}
		case sig := <-signals:
			log.WithField("signal", sig).Info("Received signal")
			goto cleanup
		}
	}

cleanup:

	log.Warning("Shutting down")
	err = c.SetStatus("Shutting down")
	if err != nil {
		log.Error("Failed to publish service status: ", err)
	}
	log.Info("Published service status")

	return nil

	/////////////////////////////////////////////////////////////

	// s := StartLorawanService(frameworkURI, serviceID, user, pass)
	// defer s.Stop()

	// s.SyncSequence()
	/* In order to bring up this service without missing any device configuration
	 * changes, we must carefully consider the startup order. The following
	 * comments will explain the the necessary startup order and what queuing
	 * must happen during each step:
	 */

	/* Finally, we start processing updates from the OpenChirp service news
	 * topic and resolving discrepancies with the lora-app-server.
	 */

	// /* Wait for SIGINT */
	// signals := make(chan os.Signal)
	// signal.Notify(signals, os.Interrupt, syscall.SIGTERM)

	// for {
	// 	select {
	// 	case update := <-s.events:
	// 		m := Config2Map(update.Config)
	// 		switch update.Type {
	// 		case framework.DeviceUpdateTypeRem:
	// 			// remove
	// 			var d DeviceConfig

	// 			// HACK, since we are not given DevEUI
	// 			for _, dev := range s.devices {
	// 				if dev.ID == update.Id {
	// 					d = dev
	// 					delete(s.devices, d.DevEUI)
	// 					break
	// 				}
	// 			}

	// 			if len(d.DevEUI) == 0 {
	// 				s.err.Printf("Framework server sent me a remove of an invalid device ID\n")
	// 				continue
	// 			}

	// 			// HACK, we need to unsubscribe

	// 			if err := s.DeleteAppEntry(d); err != nil {
	// 				s.err.Printf("Failed to delete DevEUI %s from lora app server: %v\n", d.DevEUI, err)
	// 			}

	// 			if err := s.UnlinkData(d); err != nil {
	// 				s.err.Printf("Failed to unlink data for DevEUI %s: %v\n", d.DevEUI, err)
	// 			}

	// 			s.std.Printf("Removed device %s with DevEUI %s\n", d.ID, d.DevEUI)

	// 		case framework.DeviceUpdateTypeUpd:
	// 			// remove
	// 			var d DeviceConfig

	// 			// HACK, since we are not given DevEUI
	// 			for _, dev := range s.devices {
	// 				if dev.ID == update.Id {
	// 					d = dev
	// 					delete(s.devices, d.DevEUI)
	// 					break
	// 				}
	// 			}

	// 			if len(d.DevEUI) == 0 {
	// 				s.err.Printf("Framework server sent me a remove of an invalid device ID\n")
	// 				continue
	// 			}

	// 			// HACK, we need to unsubscribe

	// 			if err := s.DeleteAppEntry(d); err != nil {
	// 				s.err.Printf("Failed to delete DevEUI %s from lora app server: %v\n", d.DevEUI, err)
	// 			}

	// 			if err := s.UnlinkData(d); err != nil {
	// 				s.err.Printf("Failed to unlink data for DevEUI %s: %v\n", d.DevEUI, err)
	// 			}

	// 			s.std.Printf("Removed device %s with DevEUI %s\n", d.ID, d.DevEUI)
	// 			fallthrough

	// 		case framework.DeviceUpdateTypeAdd:
	// 			DevEui, ok := m["DevEUI"]
	// 			if !ok {
	// 				log.Printf("Failed to register device %s - did not specify DevEUI\n", update.Id)
	// 				continue
	// 			}
	// 			AppEUI, ok := m["AppEUI"]
	// 			if !ok {
	// 				log.Printf("Failed to register device %s - did not specify AppEUI\n", update.Id)
	// 				continue
	// 			}
	// 			AppKey, ok := m["AppKey"]
	// 			if !ok {
	// 				log.Printf("Failed to register device %s - did not specify AppKey\n", update.Id)
	// 				continue
	// 			}
	// 			dev := DeviceConfig{
	// 				ID:     update.Id,
	// 				DevEUI: DevEui,
	// 				AppEUI: AppEUI,
	// 				AppKey: AppKey,
	// 			}

	// 			if err := s.PullDeviceConfig(&dev); err != nil {
	// 				s.UnlinkData(dev)
	// 				s.DeleteAppEntry(dev)
	// 				s.err.Printf("Failed to fetch device info from the framework server: %v\n", err)
	// 				continue
	// 			}
	// 			s.devices[dev.DevEUI] = dev

	// 			// Create device entry on lora app server
	// 			if err := s.CreateAppEntry(dev); err != nil {
	// 				s.err.Printf("Failed to create DevEUI %s on lora app server: %v\n", dev.DevEUI, err)
	// 				continue
	// 			}

	// 			if err := s.LinkData(dev); err != nil {
	// 				s.DeleteAppEntry(dev)
	// 				s.err.Printf("Failed to link device data: %v\n", err)
	// 				continue
	// 			}

	// 			log.Printf("Added device %s with DevEUI %s\n", update.Id, dev.DevEUI)
	// 		}
}

func main() {
	app := cli.NewApp()
	app.Name = "example-service"
	app.Usage = ""
	app.Copyright = "See https://github.com/openchirp/example-service for copyright information"
	app.Version = version
	app.Action = run
	app.Flags = []cli.Flag{
		/* Communication to OpenChirp Framework */
		cli.StringFlag{
			Name:   "framework-server",
			Usage:  "OpenChirp framework server's URI",
			Value:  "http://localhost:7000",
			EnvVar: "FRAMEWORK_SERVER",
		},
		cli.StringFlag{
			Name:   "mqtt-server",
			Usage:  "MQTT server's URI (e.g. scheme://host:port where scheme is tcp or tls)",
			Value:  "tls://localhost:1883",
			EnvVar: "MQTT_SERVER",
		},
		cli.StringFlag{
			Name:   "service-id",
			Usage:  "OpenChirp service id",
			EnvVar: "SERVICE_ID",
		},
		cli.StringFlag{
			Name:   "service-token",
			Usage:  "OpenChirp service token",
			EnvVar: "SERVICE_TOKEN",
		},
		cli.IntFlag{
			Name:   "log-level",
			Value:  4,
			Usage:  "debug=5, info=4, warning=3, error=2, fatal=1, panic=0",
			EnvVar: "LOG_LEVEL",
		},
		/* Communication to LoRaWAN App Server */
		cli.StringFlag{
			Name:   "app-mqtt-server",
			Usage:  "LoRa App Server MQTT server's URI (e.g. scheme://host:port where scheme is tcp or tls)",
			Value:  "tls://localhost:1883",
			EnvVar: "APP_MQTT_SERVER",
		},
		cli.UintFlag{
			Name:   "app-mqtt-qos",
			Usage:  "LoRa App Server MQTT server's QoS (0, 1, or 2)",
			Value:  2,
			EnvVar: "APP_MQTT_QOS",
		},
		cli.StringFlag{
			Name:   "app-mqtt-user",
			Usage:  "LoRa App Server MQTT server's username",
			EnvVar: "APP_MQTT_USER",
		},
		cli.StringFlag{
			Name:   "app-mqtt-pass",
			Usage:  "LoRa App Server MQTT server's password",
			EnvVar: "APP_MQTT_PASS",
		},
		cli.StringFlag{
			Name:   "app-grpc-server",
			Usage:  "LoRa App Server's gRPC URI",
			Value:  "locahost:8080",
			EnvVar: "APP_GRPC_SERVER",
		},
		cli.StringFlag{
			Name:   "app-grpc-user",
			Usage:  "LoRa App Server's gRPC username",
			EnvVar: "APP_GRPC_USER",
		},
		cli.StringFlag{
			Name:   "app-grpc-pass",
			Usage:  "LoRa App Server's gRPC password",
			EnvVar: "APP_GRPC_PASS",
		},
		cli.Int64Flag{
			Name:   "app-appid",
			Usage:  "LoRa App Server Appication ID",
			Value:  0,
			EnvVar: "APP_APPID",
		},
	}
	app.Run(os.Args)
}
