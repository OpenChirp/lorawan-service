package pubsubmanager

import (
	"encoding/base64"
	"fmt"
	"sync"

	"github.com/openchirp/framework"

	"github.com/openchirp/framework/pubsub"
	"github.com/openchirp/lorawan-service/lorawan/appserver"
	. "github.com/openchirp/lorawan-service/lorawan/deviceconfig"
	. "github.com/openchirp/lorawan-service/lorawan/devicemessage"
	"github.com/sirupsen/logrus"
)

const (
	topicSuffixRx    = "rawrx"
	topicSuffixTx    = "rawtx"
	topicSuffixJoin  = "joinrequest"
	topicSuffixTxAck = "txack"
)
const (
	defaultFport     = 0
	defaultConfirmed = false
)

const PubSubManagerModName = "PubSubManager"

type PubSubManager struct {
	oc                pubsub.PubSub
	app               *appserver.AppServerMQTT
	cfgFromRawtxTopic sync.Map // rawtxTopic --> *DeviceConfig
	cfgFromDeveui     sync.Map // DevEUI --> *DeviceConfig
	// deveui
	log *logrus.Entry
}

func NewPubSubManager(oc pubsub.PubSub, app *appserver.AppServerMQTT, log *logrus.Logger) *PubSubManager {
	m := new(PubSubManager)
	m.oc = oc
	m.app = app
	m.log = log.WithField("Module", PubSubManagerModName)

	/* Goroutines end when app.GetChanRX() and app.GetChanjoin() channels close */
	go m.rxhandler()
	go m.joinhandler()
	go m.txackhandler()

	return m
}

// topicRawtx returns the rawtx topic for the device config cfg
func topicRawtx(cfg *DeviceConfig) string {
	return cfg.Topic + "/" + framework.TransducerPrefix + "/" + topicSuffixTx
}

// topicRawrx returns the rawrx topic for the device config cfg
func topicRawrx(cfg *DeviceConfig) string {
	return cfg.Topic + "/" + framework.TransducerPrefix + "/" + topicSuffixRx
}

// topicJoinrequest returns the joinrequest topic for the device config cfg
func topicJoinrequest(cfg *DeviceConfig) string {
	return cfg.Topic + "/" + framework.TransducerPrefix + "/" + topicSuffixJoin
}

// topicTxAck returns the acknowledge topic for the device config cfg
func topicTxAck(cfg *DeviceConfig) string {
	return cfg.Topic + "/" + framework.TransducerPrefix + "/" + topicSuffixTxAck
}

func (m *PubSubManager) rxhandler() {
	for msg := range m.app.GetChanRX() {
		if ocinfo, ok := m.cfgFromDeveui.Load(msg.DevEUI); ok {
			// Publish to oc device
			cfg := ocinfo.(*DeviceConfig)
			topic := topicRawrx(cfg)
			msgData := base64.StdEncoding.EncodeToString(msg.Data)
			if err := m.oc.Publish(topic, msgData); err != nil {
				m.log.Fatalf("Failed to publish to %s: %v", topic, err)
				// FIXME: Need to propagate fatal error
			}
		}
	}
}

func (m *PubSubManager) joinhandler() {
	for msg := range m.app.GetChanJoin() {
		if ocinfo, ok := m.cfgFromDeveui.Load(msg.DevEUI); ok {
			// Publish to oc device
			cfg := ocinfo.(*DeviceConfig)
			topic := topicJoinrequest(cfg)
			msgData := "1"
			if err := m.oc.Publish(topic, msgData); err != nil {
				m.log.Fatalf("Failed to publish to %s: %v", topic, err)
				// FIXME: Need to propagate fatal error
			}
		}
	}
}

func (m *PubSubManager) txackhandler() {
	for msg := range m.app.GetChanAck() {
		if ocinfo, ok := m.cfgFromDeveui.Load(msg.DevEUI); ok {
			// Publish to oc device
			cfg := ocinfo.(*DeviceConfig)
			topic := topicTxAck(cfg)
			msgData := "1"
			if err := m.oc.Publish(topic, msgData); err != nil {
				m.log.Fatalf("Failed to publish to %s: %v", topic, err)
				// FIXME: Need to propagate fatal error
			}
		}
	}
}

func (m *PubSubManager) txhandler(topic string, payload []byte) {
	m.app.GetChanAck()
	if ocinfo, ok := m.cfgFromRawtxTopic.Load(topic); ok {
		cfg := ocinfo.(*DeviceConfig)
		// decode it
		data, err := base64.StdEncoding.DecodeString(string(payload))
		if err != nil {
			m.log.Fatalf("Failed to decode base64 from %s: %v", topic, err)
			return
		}
		// ship it
		m.app.GetChanTX() <- DeviceMessageData{
			DevEUI:    cfg.DevEUI,
			FPort:     defaultFport,
			Confirmed: defaultConfirmed,
			Data:      data,
		}
	}
}

func (m *PubSubManager) sanityCheckDeviceConfig(c DeviceConfig) error {
	if c.Topic == "" {
		return fmt.Errorf("Given a DeviceConfig without a Topic")
	}
	return nil
}

func (m *PubSubManager) Add(c DeviceConfig) error {
	logitem := m.log.WithFields(c.OCDeviceInfo.LogrusFields())

	logitem.Debugf("Adding device")

	if err := m.sanityCheckDeviceConfig(c); err != nil {
		return err
	}

	m.cfgFromDeveui.Store(c.DevEUI, &c)
	m.cfgFromRawtxTopic.Store(topicRawtx(&c), &c)

	logitem.Debug("Adding rawtx link")
	topic := topicRawtx(&c)
	if err := m.oc.Subscribe(topic, m.txhandler); err != nil {
		logitem.Errorf("Failed to subscribe to device tx topic %s: %v", topic, err)
		m.Remove(c)
		return err
	}

	return nil
}

func (m *PubSubManager) Remove(c DeviceConfig) error {
	logitem := m.log.WithFields(c.OCDeviceInfo.LogrusFields())

	logitem.Debug("Removing device")

	if err := m.sanityCheckDeviceConfig(c); err != nil {
		return err
	}

	m.cfgFromDeveui.Delete(c.DevEUI)
	m.cfgFromRawtxTopic.Delete(topicRawtx(&c))

	return nil
}

func (m *PubSubManager) Update(oldconfig, newconfig DeviceConfig) error {
	logitem := m.log.WithFields(logrus.Fields{
		"OldOCID":    oldconfig.ID,
		"OldOCName":  oldconfig.Name,
		"OldOCOwner": oldconfig.OwnerString(),
	})
	logitem = logitem.WithFields(newconfig.OCDeviceInfo.LogrusFields())

	logitem.Debug("Updating device")

	if err := m.sanityCheckDeviceConfig(oldconfig); err != nil {
		return err
	}

	if err := m.sanityCheckDeviceConfig(newconfig); err != nil {
		return err
	}

	m.Remove(oldconfig)
	m.Add(newconfig)

	return nil
}

func (m *PubSubManager) DebugDump() {
	logitem := m.log.WithField("Debug", "dump")

	logitem.Debugf("# Dumping ConfigFromDevEUI")
	m.cfgFromDeveui.Range(func(key, value interface{}) bool {
		devconfig := value.(*DeviceConfig)
		logitem := logitem.WithFields(devconfig.LogrusFields())
		logitem.Debugf("Key: DevEUI = %v", key)
		return true
	})

	logitem.Debugf("# Dumping ConfigFromRawRXTopic")
	m.cfgFromRawtxTopic.Range(func(key, value interface{}) bool {
		devconfig := value.(*DeviceConfig)
		logitem := logitem.WithFields(devconfig.LogrusFields())
		logitem.Debugf("Key: RawRXTopic = %v", key)
		return true
	})
}
