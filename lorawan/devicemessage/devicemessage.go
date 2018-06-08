package devicemessage

type DeviceMessageData struct {
	DevEUI    string
	Data      []byte // base64->bytes->base64 again
	Confirmed bool
	FPort     uint8
}

type DeviceMessageJoin struct {
	DevEUI string
}

type DeviceMessageAck struct {
	DevEUI string
}
