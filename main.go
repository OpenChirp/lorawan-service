package main

import (
	"io/ioutil"
	"os"
	"os/signal"
	"syscall"

	"github.com/coreos/go-systemd/daemon"

	"time"

	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
	"github.com/wercker/journalhook"
)

const (
	runningStatus           = true
	appServerJWTRefreshTime = time.Minute * time.Duration(30)
	version                 = "3.0"
)

const (
	appServerMqttPersistance = false
	configDevEUI             = "DevEUI"
	configAppKey             = "AppKey"
	configClass              = "Class"
)

const (
	deviceStatusSuccess       = "Registration successful"
	deviceStatusUpdateSuccess = "Registration update successful"
)

func run(ctx *cli.Context) error {

	systemdIntegration := ctx.Bool("systemd")

	/* Set logging level */
	log := logrus.New()
	log.SetLevel(logrus.Level(uint32(ctx.Int("log-level"))))
	if systemdIntegration {
		log.AddHook(&journalhook.JournalHook{})
		log.Out = ioutil.Discard
	}

	/* Startup LoRaWAN server */
	ls := LorawanService{
		AppGRPCServer:   ctx.String("app-grpc-server"),
		AppGRPCUser:     ctx.String("app-grpc-user"),
		AppGRPCPass:     ctx.String("app-grpc-pass"),
		AppID:           ctx.Int64("app-appid"),
		OrganizationID:  ctx.Int64("app-orgid"),
		NetworkServerID: ctx.Int64("app-netsrvid"),
		AppMQTTBroker:   ctx.String("app-mqtt-server"),
		AppMQTTUser:     ctx.String("app-mqtt-user"),
		AppMQTTPass:     ctx.String("app-mqtt-pass"),
		OCServer:        ctx.String("framework-server"),
		OCMQTTBroker:    ctx.String("mqtt-server"),
		OCID:            ctx.String("service-id"),
		OCToken:         ctx.String("service-token"),
		Log:             log,
	}
	log.Info("Starting LoRaWAN Service ")
	if err := ls.Start(); err != nil {
		return cli.NewExitError(err, 1)
	}
	log.Info("Service has started")
	if systemdIntegration {
		daemon.SdNotify(false, daemon.SdNotifyReady)
	}

	/* Setup signal channel */
	signals := make(chan os.Signal)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM, syscall.SIGUSR1)

selectagain:
	select {
	case err := <-ls.FatalError():
		ls.Stop()
		return cli.NewExitError(err, 1)
	case sig := <-signals:
		log.WithField("signal", sig).Info("Received signal")
		if sig == syscall.SIGUSR1 {
			ls.DebugDump()
			goto selectagain
		}
		goto cleanup
	}

cleanup:
	ls.Stop()
	if systemdIntegration {
		daemon.SdNotify(false, daemon.SdNotifyStopping)
	}
	return nil
}

func main() {
	app := cli.NewApp()
	app.Name = "lorawan-service"
	app.Usage = ""
	app.Copyright = "See https://github.com/openchirp/lorawan-service for copyright information"
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
		cli.BoolFlag{
			Name:   "systemd",
			Usage:  "Indicates that this service can use systemd specific interfaces.",
			EnvVar: "SYSTEMD",
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
			Usage:  "LoRa App Server Application ID",
			Value:  1,
			EnvVar: "APP_APPID",
		},
		cli.Int64Flag{
			Name:   "app-orgid",
			Usage:  "LoRa App Server Organization ID",
			Value:  1,
			EnvVar: "APP_ORGID",
		},
		cli.Int64Flag{
			Name:   "app-netsrvid",
			Usage:  "LoRa App Server Network Server ID",
			Value:  1,
			EnvVar: "APP_NETSERVERID",
		},
	}
	app.Run(os.Args)
}
