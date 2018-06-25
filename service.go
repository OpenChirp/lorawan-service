package main

import (
	"fmt"
	"sync"
	"time"

	"github.com/openchirp/framework"
	"github.com/openchirp/lorawan-service/lorawan/appserver"
	. "github.com/openchirp/lorawan-service/lorawan/deviceconfig"
	"github.com/sirupsen/logrus"
)

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

	fatalerror  chan error
	stopruntime chan bool
	runtimewait sync.WaitGroup
}

func makelogitem(log *logrus.Logger, config DeviceConfig) *logrus.Entry {
	return log.WithFields(logrus.Fields{
		"OCID":   config.ID,
		"OCName": config.Name,
	})
}

func (s *LorawanService) startOC() error {
	/* Start framework service client */
	c, err := framework.StartServiceClientStatus(
		s.OCServer,
		s.OCMQTTBroker,
		s.OCID,
		s.OCToken,
		"Unexpected disconnect!")
	if err != nil {
		s.Log.Error("Failed to StartServiceClient: ", err)
		return err
	}
	s.Log.Debug("Started service")

	/* Post service status indicating I am starting */
	err = c.SetStatus("Starting")
	if err != nil {
		s.Log.Error("Failed to publish service status: ", err)
		return err
	}
	s.Log.Debug("Published Service Status")
	s.client = c
	return nil
}

func (s *LorawanService) startAppserver() error {
	/* Connect to App Server's MQTT Broker */
	// if ctx.Uint("app-mqtt-qos") < 0 || 2 < ctx.Uint("app-mqtt-qos") {
	// 	log.Fatal("App Server QoS out of valid range")
	// 	return err
	// }
	appMqtt, err := appserver.NewAppServerMqtt(
		s.AppMQTTBroker,
		s.AppMQTTUser,
		s.AppMQTTPass,
		s.AppID)
	if err != nil {
		s.Log.Fatal("Failed to connect to App Server's MQTT Broker: ", err)
		return err
	}
	s.appMqtt = appMqtt

	/* Launch LoRaWAN Service */
	s.configs = make(map[string]DeviceConfig)
	s.app = appserver.NewAppServer(s.AppGRPCServer, s.AppID, 1, 1)
	s.Log.Debugf("Connecting to lora app server as %s:%s\n", s.AppGRPCUser, s.AppGRPCPass)
	if err := s.app.Login(s.AppGRPCUser, s.AppGRPCPass); err != nil {
		s.Log.Fatalf("Failed Login to App Server: %v\n", err)
	}
	if err := s.app.Connect(); err != nil {
		s.Log.Fatalf("Failed Connect to App Server: %v\n", err)
	}

	return nil
}

func (s *LorawanService) startOCUpdateStream() error {
	s.Log.Debug("Starting device updates stream")
	updates, err := s.client.StartDeviceUpdates()
	if err != nil {
		s.Log.Error("Failed to start device updates stream: ", err)
		return err
	}
	s.updates = updates
	return nil
}

func (s *LorawanService) syncConfigs() error {
	// fetch initial service configs
	s.Log.Debug("Fetching framework initial device configs")
	configUpdates, err := s.client.FetchDeviceConfigsAsUpdates()
	if err != nil {
		s.Log.Error("Failed to fetch initial device configs: ", err)
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
			s.Log.Info(err)
			continue
		}
		configs = append(configs, devconfig)

		s.Log.WithField("deveui", devconfig.DevEUI).Debug("Received DevConfig: ", devconfig)
	}

	err = s.client.SetStatus("Synchronizing initial registered devices and app server")
	if err != nil {
		s.Log.Fatal("Failed to publish service status: ", err)
		return err
	}
	s.Log.Debug("Synchronizing initial registered devices and app server")

	cerrors, err := s.app.DeviceRegistrationSync(configs)
	if err != nil {
		s.Log.Fatal(err)
		return err
	}

	// report any config sync errors
	for i, e := range cerrors {
		devConfig := configs[i]
		logitem := makelogitem(s.Log, devConfig)
		if e != nil {
			logitem.Infof("Failed to add or update config: %v", e)
			if err := s.client.SetDeviceStatus(devConfig.ID, e); err != nil {
				logitem.Errorf("Failed to post device status: %v", err)
			}
		} else {
			if err := s.client.SetDeviceStatus(devConfig.ID, deviceStatusSuccess); err != nil {
				logitem.Errorf("Failed to post device status: %v", err)
			}
		}
	}

	return nil
}

func (s *LorawanService) runtime() {
	defer s.runtimewait.Done()

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
			s.Log.Debug("Reconnecting to app server")
			if err := s.app.ReLogin(); err != nil {
				s.Log.Fatalf("Failed to relogin the app server: %v\n", err)
				err = s.client.SetStatus("Failed to relogin to app server: ", err)
				if err != nil {
					s.Log.Error("Failed to publish service status: ", err)
				}
			}
		}
	}
}

func (s *LorawanService) processUpdate(update framework.DeviceUpdate) error {
	/* If runningStatus is set, post a service status as an alive msg */
	if runningStatus {
		if err := s.client.SetStatus("Running"); err != nil {
			s.Log.Error("Failed to publish service status: ", err)
			return err
		}
		s.Log.Debug("Published Service Status")
	}

	logitem := s.Log.WithFields(logrus.Fields{
		"UpdateType": update.Type,
		"OCID":       update.Id,
	})

	processadd := func() error {
		logitem.Debug("Fetching device info")
		devconfig, err := DeviceUpdateAdapter{update}.GetDeviceConfig(s.client)
		if err != nil {
			// Had problem fetching device info
			logitem.Errorf("Failed to fetch OC info: %v", err)
			return nil
		}
		logitem.Debug("Process Add")
		if err := s.app.DeviceRegister(devconfig); err != nil {
			logitem.Infof("Failed to add device config: %v", err)
			if e := s.client.SetDeviceStatus(devconfig.ID, err); e != nil {
				logitem.Errorf("Failed to post device status: %v", e)
			}
			return nil
		}
		s.configs[devconfig.ID] = devconfig
		if err := s.client.SetDeviceStatus(devconfig.ID, deviceStatusSuccess); err != nil {
			logitem.Errorf("Failed to post device status: %v", err)
		}
		return nil
	}

	processremove := func() error {
		logitem.Debug("Fetching device info")
		devconfig, err := DeviceUpdateAdapter{update}.GetDeviceConfig(nil)
		if err != nil {
			// Had problem fetching device info
			logitem.Errorf("Failed to fetch OC info: %v", err)
			return nil
		}
		oldconfig := s.configs[devconfig.ID]
		logitem.Debug("Process Remove")
		if err := s.app.DeviceDeregister(oldconfig); err != nil {
			logitem.Infof("Failed to deregister device config: %v", err)
			return nil
		}
		return nil
	}

	processupdate := func() error {
		logitem.Debug("Fetching device info")
		devconfig, err := DeviceUpdateAdapter{update}.GetDeviceConfig(s.client)
		if err != nil {
			// Had problem fetching device info
			logitem.Errorf("Failed to fetch OC info: %v", err)
			return nil
		}
		logitem.Debug("Process Update")
		if oldconfig, ok := s.configs[devconfig.ID]; ok {
			if err := s.app.DeviceUpdate(oldconfig, devconfig); err != nil {
				logitem.Infof("Failed to update device config: %v", err)
				if e := s.client.SetDeviceStatus(devconfig.ID, err); e != nil {
					logitem.Errorf("Failed to post device status: %v", e)
				}
				return nil
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
	if err := s.startOC(); err != nil {
		return fmt.Errorf("Failed to start OC connection: %v", err)
	}

	if err := s.startAppserver(); err != nil {
		return fmt.Errorf("Failed to start app server connection: %v", err)
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
	s.Log.Debug("Published Service Status")

	s.stopruntime = make(chan bool)
	s.runtimewait.Add(1)
	go s.runtime()

	return nil
}

func (s *LorawanService) Stop() error {
	s.Log.Warning("Shutting down")
	if err := s.client.SetStatus("Shutting down"); err != nil {
		s.Log.Error("Failed to publish service status: ", err)
	}
	s.Log.Info("Published service status")

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
	s.app.DebugDump()
}
