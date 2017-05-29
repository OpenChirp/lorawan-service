package main

import (
	"flag"
	"fmt"

	"log"

	"github.com/openchirp/framework"
	"github.com/openchirp/framework/rest"
)

const (
	defaultframeworkhost = "http://localhost"
)

/* Options to be filled in by arguments */
var hostUri string
var user string
var pass string
var serviceId string

/* Setup argument flags and help prompt */
func init() {
	flag.StringVar(&hostUri, "host", defaultframeworkhost, "Sets the framework host's URI")
	flag.StringVar(&user, "user", "", "Sets the framework username")
	flag.StringVar(&pass, "pass", "", "Sets the framework password")
	flag.StringVar(&serviceId, "serviceid", "", "Sets the Service ID")
}

func main() {
	/* Parse Arguments */
	flag.Parse()

	fmt.Println("# Starting up")
	fmt.Println(hostUri)
	fmt.Println(user)
	fmt.Println(pass)
	fmt.Println(serviceId)

	host := rest.NewHost(hostUri)
	host.Login(user, pass)
	service, err := framework.StartService(host, serviceId)
	if err != nil {
		log.Fatalf("Failed to start service: %v\n", err)
	}
	properties := service.GetProperties()

	appServerTarget, ok := properties["AppServerTarget"]
	if !ok {
		log.Fatalf("Failed to detect LoRa App Server Target URI\n")
	}
	a := NewAppServer(appServerTarget)
	a.Login(properties["AppServerUser"], properties["AppServerPass"])
	a.Connect()
	defer a.Disconnect()
	a.GetUsers()
	fmt.Println("Done")

	/* In order bring up this service without missing any device configuration
	 * changes, we must carefully consider the startup order. The following
	 * comments will explain the the necessary startup order and what queuing
	 * must happen during each step:
	 */

	/* The first order of business is to fetch the current registered devices
	 * on the lora-app-server and import them as devices and configuration.
	 */

	/* Next, we will subscribe to the OpenChirp service news topic and queue
	 * updates that we are sent
	 */

	/* We then will fetch the static configuration from the OpenChirp
	 * framework server and resolve discrepancies with the config from the
	 * lora-app-server
	 */

	/* Finally, we start processing updates from the OpenChirp service news
	 * topic and pushing discrepancies to the lora-app-server
	 */
}
