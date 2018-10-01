[![Build Status](https://travis-ci.org/OpenChirp/lorawan-service.svg?branch=master)](https://travis-ci.org/OpenChirp/lorawan-service)

# LoRaWAN Service
This is the OpenChirp service that manages the synchronization to Brocaars' lora-app-server
for LoRaWAN clients.

# Development Notes
This service always makes an additional REST request to fetch device information
upon receiving a new device to link.
It uses the device's pubsub topic, name, and owner info.

# Service Config

| Key Name | Required? | Key Description | Key Example |
| -------- | --------- | --------------- | ----------- |
| `DevEUI` | Required  | A device's unique identifier (8 byte hexadecimal) | 1122334455667788
| `AppKey` | Required  | The encryption key used while joining (8 byte hexadecimal) | 11223344556677881122334455667788
| `Class`  | Optional  | The protocol class to use (A, B, or C) | A
| `AppEUI` | No Longer Used | An application identifier (8 byte hexadecimal) | 0000000000000000
