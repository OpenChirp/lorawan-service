package devicemessage

import "github.com/openchirp/lorawan-service/lorawan/deviceconfig"

type DeviceMessageData struct {
	DevEUI    string
	Data      []byte // base64->bytes->base64 again
	Confirmed bool
	FPort     uint8
}

func NewDeviceMessageData(DevEUI string, Data []byte, Confirmed bool, FPort uint8) DeviceMessageData {
	return DeviceMessageData{
		DevEUI:    deviceconfig.LorawanConfigConvertCase(DevEUI),
		Data:      Data,
		Confirmed: Confirmed,
		FPort:     FPort,
	}
}

type DeviceMessageJoin struct {
	DevEUI string
}

func NewDeviceMessageJoin(DevEUI string) DeviceMessageJoin {
	return DeviceMessageJoin{deviceconfig.LorawanConfigConvertCase(DevEUI)}
}

type DeviceMessageAck struct {
	DevEUI string
}

func NewDeviceMessageAck(DevEUI string) DeviceMessageAck {
	return DeviceMessageAck{deviceconfig.LorawanConfigConvertCase(DevEUI)}
}
