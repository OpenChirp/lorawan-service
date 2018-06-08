// June 6, 2018
// Craig Hesling
//
//
package appserver

/*
 * This file contains snippets from
 * github.com/brocaar/lora-app-server/internal/handler/models.go
 * for interfacing purposes.
 *
 * To keep this implementation simple, I have put the relevant interface types
 * inside the functions that use them.
 */

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/brocaar/lorawan"
	"github.com/openchirp/framework/pubsub"
	. "github.com/openchirp/lorawan-service/lorawan/devicemessage"
	"golang.org/x/net/context"
)

const (
	defaultQOS         = pubsub.QoSAtLeastOnce
	defaultPersistance = false
	channelBuffering   = 0
)

type AppServerMQTT struct {
	appID    int64
	client   *pubsub.MQTTClient
	tx       chan DeviceMessageData
	rx       chan DeviceMessageData
	join     chan DeviceMessageJoin
	ack      chan DeviceMessageAck
	txCtx    context.Context
	txCancel context.CancelFunc

	log *logrus.Logger
}

func NewAppServerMqtt(uri, user, pass string, appID int64) (*AppServerMQTT, error) {
	am := new(AppServerMQTT)
	am.appID = appID
	am.log = logrus.New()
	am.log.Level = 5

	/* Setup MQTT Client */
	client, err := pubsub.NewMQTTClient(uri, user, pass, defaultQOS, defaultPersistance)
	if err != nil {
		return nil, err
	}
	am.client = client

	/* Setup Channels */
	am.tx = make(chan DeviceMessageData, channelBuffering)
	am.rx = make(chan DeviceMessageData, channelBuffering)
	am.join = make(chan DeviceMessageJoin, channelBuffering)
	am.ack = make(chan DeviceMessageAck, channelBuffering)

	/* Launch TX Channel Goroutine */
	txCtx, txCancel := context.WithCancel(context.Background())
	am.txCtx = txCtx
	am.txCancel = txCancel
	go func(am *AppServerMQTT) {
		for {
			select {
			case <-am.txCtx.Done():
				return
			case msg := <-am.tx:
				am.onTx(msg)
			}
		}
	}(am)

	/* Subscribe to Inbound Topics */
	var topic string
	topic = fmt.Sprintf("application/%v/device/+/rx", am.appID)
	if err := am.client.Subscribe(topic, am.onRx); err != nil {
		am.Disconnect()
		return nil, err
	}

	topic = fmt.Sprintf("application/%v/device/+/join", am.appID)
	if err := am.client.Subscribe(topic, am.onJoin); err != nil {
		am.Disconnect()
		return nil, err
	}

	topic = fmt.Sprintf("application/%v/device/+/ack", am.appID)
	if err := am.client.Subscribe(topic, am.onAck); err != nil {
		am.Disconnect()
		return nil, err
	}

	topic = fmt.Sprintf("application/%v/device/+/error", am.appID)
	if err := am.client.Subscribe(topic, am.onError); err != nil {
		am.Disconnect()
		return nil, err
	}

	return am, nil
}

func (am *AppServerMQTT) Disconnect() {
	am.txCancel()
	am.client.Disconnect()
}

func (am *AppServerMQTT) GetChanRX() <-chan DeviceMessageData {
	return am.rx
}

func (am *AppServerMQTT) GetChanTX() chan<- DeviceMessageData {
	return am.tx
}

func (am *AppServerMQTT) GetChanJoin() <-chan DeviceMessageJoin {
	return am.join
}

func (am *AppServerMQTT) GetChanAck() <-chan DeviceMessageAck {
	return am.ack
}

func (am *AppServerMQTT) onTx(msg DeviceMessageData) {
	deveui := strings.ToLower(msg.DevEUI)
	topic := fmt.Sprintf("application/%v/device/%s/tx", am.appID, deveui)

	var pkt struct {
		ApplicationID int64           `json:"applicationID,string"`
		DevEUI        lorawan.EUI64   `json:"devEUI"`
		Reference     string          `json:"reference"`
		Confirmed     bool            `json:"confirmed"`
		FPort         uint8           `json:"fPort"`
		Data          []byte          `json:"data"`
		Object        json.RawMessage `json:"object"`
	}

	if err := pkt.DevEUI.UnmarshalText([]byte(msg.DevEUI)); err != nil {
		logitem := am.log.WithError(err).WithField("topic", topic)
		logitem.Error("Failed to decode DevEUI")
		return
	}
	pkt.FPort = msg.FPort
	pkt.Confirmed = msg.Confirmed
	pkt.Data = msg.Data

	payload, err := json.Marshal(&pkt)
	if err != nil {
		logitem := am.log.WithError(err).WithField("topic", topic)
		logitem.Error("Failed to encode TX packet")
		return
	}

	am.client.Publish(topic, payload)
}

func (am *AppServerMQTT) onRx(topic string, payload []byte) {

	// DataRate contains the data-rate related fields.
	type DataRate struct {
		Modulation   string `json:"modulation"`
		Bandwidth    int    `json:"bandwidth"`
		SpreadFactor int    `json:"spreadFactor,omitempty"`
		Bitrate      int    `json:"bitrate,omitempty"`
	}

	// RXInfo contains the RX information.
	type RXInfo struct {
		MAC       lorawan.EUI64 `json:"mac"`
		Time      *time.Time    `json:"time,omitempty"`
		RSSI      int           `json:"rssi"`
		LoRaSNR   float64       `json:"loRaSNR"`
		Name      string        `json:"name"`
		Latitude  float64       `json:"latitude"`
		Longitude float64       `json:"longitude"`
		Altitude  float64       `json:"altitude"`
	}

	// TXInfo contains the TX information.
	type TXInfo struct {
		Frequency int      `json:"frequency"`
		DataRate  DataRate `json:"dataRate"`
		ADR       bool     `json:"adr"`
		CodeRate  string   `json:"codeRate"`
	}

	var pkt struct {
		ApplicationID       int64         `json:"applicationID,string"`
		ApplicationName     string        `json:"applicationName"`
		DeviceName          string        `json:"deviceName"`
		DevEUI              lorawan.EUI64 `json:"devEUI"`
		DeviceStatusBattery *int          `json:"deviceStatusBattery,omitempty"`
		DeviceStatusMargin  *int          `json:"deviceStatusMargin,omitempty"`
		RXInfo              []RXInfo      `json:"rxInfo,omitempty"`
		TXInfo              TXInfo        `json:"txInfo"`
		FCnt                uint32        `json:"fCnt"`
		FPort               uint8         `json:"fPort"`
		Data                []byte        `json:"data"`
		Object              interface{}   `json:"object,omitempty"`
	}

	if err := json.Unmarshal(payload, &pkt); err != nil {
		logitem := am.log.WithError(err).WithField("topic", topic)
		logitem.Error("Failed to decode RX packet")
		return
	}
	am.rx <- DeviceMessageData{
		DevEUI: fmt.Sprint(pkt.DevEUI),
		FPort:  pkt.FPort,
		Data:   pkt.Data,
	}
}

func (am *AppServerMQTT) onJoin(topic string, payload []byte) {
	var pkt struct {
		ApplicationID   int64           `json:"applicationID,string"`
		ApplicationName string          `json:"applicationName"`
		DeviceName      string          `json:"deviceName"`
		DevEUI          lorawan.EUI64   `json:"devEUI"`
		DevAddr         lorawan.DevAddr `json:"devAddr"`
	}
	if err := json.Unmarshal(payload, &pkt); err != nil {
		logitem := am.log.WithError(err).WithField("topic", topic)
		logitem.Error("Failed to decode Join packet")
		return
	}

	am.join <- DeviceMessageJoin{
		DevEUI: fmt.Sprint(pkt.DevEUI),
	}
}

func (am *AppServerMQTT) onAck(topic string, payload []byte) {
	var pkt struct {
		ApplicationID   int64         `json:"applicationID,string"`
		ApplicationName string        `json:"applicationName"`
		DeviceName      string        `json:"deviceName"`
		DevEUI          lorawan.EUI64 `json:"devEUI"`
		Reference       string        `json:"reference"`
		Acknowledged    bool          `json:"acknowledged"`
		FCnt            uint32        `json:"fCnt"`
	}
	if err := json.Unmarshal(payload, &pkt); err != nil {
		logitem := am.log.WithError(err).WithField("topic", topic)
		logitem.Error("Failed to decode Ack packet")
		return
	}
	am.ack <- DeviceMessageAck{
		DevEUI: fmt.Sprint(pkt.DevEUI),
	}
}

func (am *AppServerMQTT) onError(topic string, payload []byte) {
	var pkt struct {
		ApplicationID   int64         `json:"applicationID,string"`
		ApplicationName string        `json:"applicationName"`
		DeviceName      string        `json:"deviceName"`
		DevEUI          lorawan.EUI64 `json:"devEUI"`
		Type            string        `json:"type"`
		Error           string        `json:"error"`
		FCnt            uint32        `json:"fCnt,omitempty"`
	}
	if err := json.Unmarshal(payload, &pkt); err != nil {
		logitem := am.log.WithError(err).WithField("topic", topic)
		logitem.Error("Failed to decode Error packet")
		return
	}
	// TODO: Implement some error action
	return
}
