package main

import (
	"fmt"
	"sync"
	"time"

	"github.com/openchirp/framework"
	"github.com/openchirp/framework/pubsub"
	"github.com/openchirp/lorawan-service/lorawan/appserver"
	. "github.com/openchirp/lorawan-service/lorawan/deviceconfig"
	"github.com/openchirp/lorawan-service/lorawan/pubsubmanager"
	"github.com/sirupsen/logrus"
)

const LorawanServiceModName = "LorawanService"

type LorawanService struct {
	AppGRPCServer string
	AppGRPCUser   string
	AppGRPCPass   string
	AppID         int64
	AppMQTTBroker string
	AppMQTTUser   string
	AppMQTTPass   string

	OCServer     string
	OCMQTTBroker string
	OCID         string
	OCToken      string

	Log *logrus.Logger

	client  *framework.ServiceClient
	updates <-chan framework.DeviceUpdate
	app     *appserver.AppServer
	appMqtt *appserver.AppServerMQTT
	configs map[string]DeviceConfig // OCID --> DeviceConfig

	ocpubsub  *pubsub.PubSub
	pubsubman *pubsubmanager.PubSubManager

	fatalerror  chan error
	stopruntime chan bool
	runtimewait sync.WaitGroup
}

func makelogitem(log *logrus.Logger, config DeviceConfig) *logrus.Entry {
	return log.WithFields(logrus.Fields{
		"Module":  LorawanServiceModName,
		"OCID":    config.ID,
		"OCName":  config.Name,
		"OCOwner": config.OwnerString(),
	})
}

func (s *LorawanService) startOC() error {
	logitem := s.Log.WithField("Module", LorawanServiceModName)

	/* Start framework service client */
	c, err := framework.StartServiceClientStatus(
		s.OCServer,
		s.OCMQTTBroker,
		s.OCID,
		s.OCToken,
		"Unexpected disconnect!")
	if err != nil {
		logitem.Error("Failed to StartServiceClient: ", err)
		return err
	}
	logitem.Debug("Started service")

	/* Post service status indicating I am starting */
	err = c.SetStatus("Starting")
	if err != nil {
		logitem.Error("Failed to publish service status: ", err)
		return err
	}
	logitem.Debug("Published Service Status")
	s.client = c
	return nil
}

func (s *LorawanService) startAppserver() error {
	logitem := s.Log.WithField("Module", LorawanServiceModName)

	/* Connect to App Server's MQTT Broker */
	// if ctx.Uint("app-mqtt-qos") < 0 || 2 < ctx.Uint("app-mqtt-qos") {
	// 	log.Fatal("App Server QoS out of valid range")
	// 	return err
	// }
	appMqtt, err := appserver.NewAppServerMqtt(
		s.AppMQTTBroker,
		s.AppMQTTUser,
		s.AppMQTTPass,
		s.AppID,
		s.Log)
	if err != nil {
		logitem.Fatal("Failed to connect to App Server's MQTT Broker: ", err)
		return err
	}
	s.appMqtt = appMqtt

	/* Launch LoRaWAN Service */
	s.configs = make(map[string]DeviceConfig)
	s.app = appserver.NewAppServer(s.AppGRPCServer, s.AppID, 1, 1, s.Log)
	logitem.Debugf("Connecting to lora app server as %s:%s", s.AppGRPCUser, s.AppGRPCPass)
	if err := s.app.Login(s.AppGRPCUser, s.AppGRPCPass); err != nil {
		logitem.Fatalf("Failed Login to App Server: %v\n", err)
	}
	if err := s.app.Connect(); err != nil {
		logitem.Fatalf("Failed Connect to App Server: %v\n", err)
	}

	return nil
}

func (s *LorawanService) startPubsubManager() error {
	s.pubsubman = pubsubmanager.NewPubSubManager(s.client, s.appMqtt, s.Log)
	return nil
}

func (s *LorawanService) startOCUpdateStream() error {
	logitem := s.Log.WithField("Module", LorawanServiceModName)

	logitem.Debug("Starting device updates stream")
	updates, err := s.client.StartDeviceUpdates()
	if err != nil {
		logitem.Error("Failed to start device updates stream: ", err)
		return err
	}
	s.updates = updates
	return nil
}

func (s *LorawanService) syncConfigs() error {
	logitem := s.Log.WithField("Module", LorawanServiceModName)

	// fetch initial service configs
	logitem.Debug("Fetching framework initial device configs")
	configUpdates, err := s.client.FetchDeviceConfigsAsUpdates()
	if err != nil {
		logitem.Error("Failed to fetch initial device configs: ", err)
		return err
	}

	// sync

	// Since we could fail to fetch info for a device, we need to leave the
	// possibility of less DeviceConfig being created
	configs := make([]DeviceConfig, 0, len(configUpdates))
	for i := range configUpdates {
		devconfig, err := DeviceUpdateAdapter{configUpdates[i]}.GetDeviceConfig(s.client)
		if err != nil {
			// Had problem fetching device info
			logitem.Info(err)
			continue
		}
		configs = append(configs, devconfig)

		// Local logitem
		logitem := logitem.WithFields(devconfig.OCDeviceInfo.LogrusFields())
		logitem = logitem.WithField("DevEUI", devconfig.DevEUI)
		logitem.Debug("Received DevConfig")
	}

	err = s.client.SetStatus("Synchronizing initial registered devices and app server")
	if err != nil {
		logitem.Fatal("Failed to publish service status: ", err)
		return err
	}
	logitem.Debug("Synchronizing initial registered devices and app server")

	cerrors, err := s.app.DeviceRegistrationSync(configs)
	if err != nil {
		logitem.Fatal(err)
		return err
	}

	// report any config sync errors -- add DeviceConfigs that succeed
	for i, e := range cerrors {
		devConfig := configs[i]
		logitem := makelogitem(s.Log, devConfig)
		if e != nil {
			logitem.Infof("Failed to add or update config: %v", e)
			if err := s.client.SetDeviceStatus(devConfig.ID, e); err != nil {
				logitem.Errorf("Failed to post device status: %v", err)
			}
		} else {
			s.configs[devConfig.ID] = devConfig
			s.pubsubman.Add(devConfig)
			if err := s.client.SetDeviceStatus(devConfig.ID, deviceStatusSuccess); err != nil {
				logitem.Errorf("Failed to post device status: %v", err)
			}
		}
	}

	return nil
}

func (s *LorawanService) runtime() {
	defer s.runtimewait.Done()

	logitem := s.Log.WithField("Module", LorawanServiceModName)

	/* Start runtime event loop */
	for {
		select {
		case <-s.stopruntime:
			return
		case update := <-s.updates:
			if err := s.processUpdate(update); err != nil {
				s.fatalerror <- err
				<-s.stopruntime
				return
			}
		case <-time.After(appServerJWTRefreshTime):
			logitem.Debug("Reconnecting to app server")
			if err := s.app.ReLogin(); err != nil {
				logitem.Fatalf("Failed to relogin the app server: %v\n", err)
				err = s.client.SetStatus("Failed to relogin to app server: ", err)
				if err != nil {
					logitem.Error("Failed to publish service status: ", err)
				}
			}
		}
	}
}

func (s *LorawanService) processUpdate(update framework.DeviceUpdate) error {
	logitem := s.Log.WithFields(logrus.Fields{
		"Module":     LorawanServiceModName,
		"UpdateType": update.Type,
		"OCID":       update.Id,
	})

	/* If runningStatus is set, post a service status as an alive msg */
	if runningStatus {
		if err := s.client.SetStatus("Running"); err != nil {
			logitem.Error("Failed to publish service status: ", err)
			return err
		}
		logitem.Debug("Published Service Status")
	}

	logitem.Debug("Fetching device info")
	devconfig, err := DeviceUpdateAdapter{update}.GetDeviceConfig(s.client)
	if err != nil {
		// Had problem fetching device info
		logitem.Errorf("Failed to fetch OC info: %v", err)
		return nil
	}

	logitem = logitem.WithFields(devconfig.LogrusFields())

	processadd := func() error {
		logitem.Debug("Process Add")
		if err := s.app.DeviceRegister(devconfig); err != nil {
			logitem.Infof("Failed to add device config: %v", err)
			if e := s.client.SetDeviceStatus(devconfig.ID, err); e != nil {
				logitem.Errorf("Failed to post device status: %v", e)
			}
			return nil
		}
		s.configs[devconfig.ID] = devconfig
		if err := s.pubsubman.Add(devconfig); err != nil {
			logitem.Errorf("Failed to add device to pubsubmanager: %v", err)
		}
		if err := s.client.SetDeviceStatus(devconfig.ID, deviceStatusSuccess); err != nil {
			logitem.Errorf("Failed to post device status: %v", err)
		}

		return nil
	}

	processremove := func() error {
		logitem.Debug("Process Remove")
		defer delete(s.configs, devconfig.ID)
		if err := s.pubsubman.Remove(devconfig); err != nil {
			logitem.Errorf("Failed to remove device from pubsubmanager: %v", err)
		}
		oldconfig := s.configs[devconfig.ID]
		if err := s.app.DeviceDeregister(oldconfig); err != nil {
			logitem.Infof("Failed to deregister device config: %v", err)
			return nil
		}
		return nil
	}

	processupdate := func() error {
		logitem.Debug("Process Update")
		if oldconfig, ok := s.configs[devconfig.ID]; ok {
			if err := s.app.DeviceUpdate(oldconfig, devconfig); err != nil {
				logitem.Infof("Failed to update device config: %v", err)
				if e := s.client.SetDeviceStatus(devconfig.ID, err); e != nil {
					logitem.Errorf("Failed to post device status: %v", e)
				}
				return nil
			}
			if err := s.pubsubman.Update(oldconfig, devconfig); err != nil {
				logitem.Errorf("Failed to update device in pubsubmanager: %v", err)
			}
			s.configs[devconfig.ID] = devconfig
			if err := s.client.SetDeviceStatus(devconfig.ID, deviceStatusSuccess); err != nil {
				logitem.Errorf("Failed to post device status: %v", err)
			}
		} else {
			logitem.Info("Diverting to Add process")
			return processadd()
		}
		return nil
	}

	switch update.Type {
	case framework.DeviceUpdateTypeAdd:
		return processadd()
	case framework.DeviceUpdateTypeRem:
		return processremove()
	case framework.DeviceUpdateTypeUpd:
		return processupdate()
	case framework.DeviceUpdateTypeErr:
		logitem.Errorf(update.Error())
	}

	return nil
}

func (s *LorawanService) Start() error {
	logitem := s.Log.WithField("Module", LorawanServiceModName)

	if err := s.startOC(); err != nil {
		return fmt.Errorf("Failed to start OC connection: %v", err)
	}

	if err := s.startAppserver(); err != nil {
		return fmt.Errorf("Failed to start app server connection: %v", err)
	}

	if err := s.startPubsubManager(); err != nil {
		return fmt.Errorf("Failed to start pubsub manager(bridge): %v", err)
	}

	if err := s.startOCUpdateStream(); err != nil {
		return fmt.Errorf("Failed to start OC device update stream: %v", err)
	}

	if err := s.syncConfigs(); err != nil {
		return fmt.Errorf("Failed to sync configs: %v", err)
	}

	/* Post service status indicating I started */
	if err := s.client.SetStatus("Started"); err != nil {
		return fmt.Errorf("Failed to publish service status: %v", err)
	}
	logitem.Debug("Published Service Status")

	s.stopruntime = make(chan bool)
	s.runtimewait.Add(1)
	go s.runtime()

	return nil
}

func (s *LorawanService) Stop() error {
	logitem := s.Log.WithField("Module", LorawanServiceModName)

	logitem.Warning("Shutting down")
	if err := s.client.SetStatus("Shutting down"); err != nil {
		logitem.Error("Failed to publish service status: ", err)
	}
	logitem.Info("Published service status")

	s.stopruntime <- true
	close(s.stopruntime)
	s.client.StopDeviceUpdates()
	s.client.StopClient()
	s.appMqtt.Disconnect()
	err := s.app.Disconnect()
	s.runtimewait.Wait()
	return err
}

func (s *LorawanService) FatalError() <-chan error {
	return s.fatalerror
}

func (s *LorawanService) DebugDump() {
	// Temporarily change log level to info
	originalLevel := s.Log.Level
	s.Log.Level = logrus.DebugLevel

	s.app.DebugDump()
	s.pubsubman.DebugDump()

	logitem := s.Log.WithField("Module", LorawanServiceModName).WithField("Debug", "dump")
	logitem.Debugf("# Dumping ID to Config Maps")
	for id, config := range s.configs {
		logitem := logitem.WithFields(config.LogrusFields())
		logitem.Debug("KEY: ID ", id)
	}

	s.Log.Level = originalLevel
}
