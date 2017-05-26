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
	fmt.Println("Hello world")
	host := rest.NewHost(hostUri)
	service := framework.StartService(host, serviceId)

	appServerTarget, ok := service.GetProperties()["AppServerTarget"]
	if !ok {
		log.Fatalf("Failed to detect LoRa App Server Target URI\n")
	}
	a := NewAppServer()
	a.Login(user, pass)
	a.Connect()
	a.GetUsers()
	a.Disconnect()
	fmt.Println("Done")
}
