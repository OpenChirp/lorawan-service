# LoRaWAN Service
This is the OpenChirp service that manages the synchronization to Brocaars' lora-app-server
for LoRaWAN clients.

# Development Notes
This service always makes an additional REST request to fetch device information
upon receiving a new device to link.
It uses the device's pubsub topic, name, and owner info.