package main

import (
	"os"
	"os/signal"
	"syscall"

	"time"

	"github.com/openchirp/framework"
	"github.com/openchirp/framework/pubsub"
	"github.com/openchirp/lorawan-service/lorawan"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
)

const (
	runningStatus           = true
	appServerJWTRefreshTime = time.Minute * time.Duration(30)
	version                 = "2.0"
)

const (
	appServerMqttPersistance = false
	configDevEUI             = "DevEUI"
	configAppEUI             = "AppEUI"
	configAppKey             = "AppKey"
	configClass              = "Class"
)

const (
	defaultLorawanClass = lorawan.LorawanClassA
)

func run(ctx *cli.Context) error {

	/* Set logging level */
	log := logrus.New()
	log.SetLevel(logrus.Level(uint32(ctx.Int("log-level"))))

	log.Info("Starting LoRaWAN Service ")

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

	appServerTarget := ctx.String("app-grpc-server")
	appServerUser := ctx.String("app-grpc-user")
	appServerPass := ctx.String("app-grpc-pass")
	appAppId := ctx.Int64("app-appid")

	app := lorawan.NewAppServer(appServerTarget)
	log.Debugf("Connecting to lora app server as %s:%s\n", appServerUser, appServerPass)
	if err := app.Login(appServerUser, appServerPass); err != nil {
		log.Fatalf("Failed Login to App Server: %v\n", err)
	}
	if err := app.Connect(); err != nil {
		log.Fatalf("Failed Connect to App Server: %v\n", err)
	}
	defer app.Disconnect()

	lwManager := lorawan.NewManager(
		pubsub.NewBridge(c, appMqtt, log),
		app,
		appAppId,
		func(config lorawan.DeviceConfig, str string) {
			err := c.SetDeviceStatus(config.ID, str)
			if err != nil {
				log.Errorf("Failed to publish status for deviceid \"%s\": %v", config.ID, err)
			}
		},
		log,
	)

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

	// sync

	// Since we could fail to fetch info for a device, we need to leave the
	// possibility of less DeviceConfig being created
	configs := make([]lorawan.DeviceConfig, 0, len(configUpdates))
	for i := range configUpdates {
		devconfig, err := DeviceUpdateAdapter{configUpdates[i]}.GetLorawanDeviceConfig(c)
		if err != nil {
			// Had problem fetching device info
			log.Info(err)
			continue
		}
		configs = append(configs, devconfig)

		log.WithField("deveui", devconfig.DevEUI).Debug("Received DevConfig: ", devconfig)
	}

	err = c.SetStatus("Synchronizing initial registered devices and app server")
	if err != nil {
		log.Fatal("Failed to publish service status: ", err)
		return cli.NewExitError(nil, 1)
	}
	log.Debug("Synchronizing initial registered devices and app server")

	err = lwManager.Sync(configs)
	if err != nil {
		log.Fatal(err)
		return cli.NewExitError(nil, 1)
	}
	configs = nil
	configUpdates = nil

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

			switch update.Type {
			case framework.DeviceUpdateTypeAdd:
				logitem.Debug("Fetching device info")
				devconfig, err := DeviceUpdateAdapter{update}.GetLorawanDeviceConfig(c)
				if err != nil {
					// Had problem fetching device info
					logitem.Info(err)
					continue
				}
				logitem.Debug("Process Add")
				lwManager.ProcessAdd(devconfig)

			case framework.DeviceUpdateTypeRem:
				logitem.Debug("Fetching device info")
				devconfig, err := DeviceUpdateAdapter{update}.GetLorawanDeviceConfig(nil)
				if err != nil {
					// Had problem fetching device info
					logitem.Info(err)
					continue
				}
				logitem.Debug("Process Remove")
				lwManager.ProcessRemove(devconfig)

			case framework.DeviceUpdateTypeUpd:
				logitem.Debug("Fetching device info")
				devconfig, err := DeviceUpdateAdapter{update}.GetLorawanDeviceConfig(c)
				if err != nil {
					// Had problem fetching device info
					logitem.Info(err)
					continue
				}
				logitem.Debug("Process Update")
				lwManager.ProcessUpdate(devconfig)

			case framework.DeviceUpdateTypeErr:
				logitem.Errorf(update.Error())
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
