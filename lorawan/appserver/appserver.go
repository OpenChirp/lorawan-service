package appserver

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"

	"errors"

	"encoding/json"

	"encoding/base64"

	pb "github.com/brocaar/lora-app-server/api"
	"github.com/openchirp/lorawan-service/lorawan/utils"
	"github.com/sirupsen/logrus"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

const (
	requestLimit             = 2000000
	defaultFport             = 2
	defaultConfirmedDownlink = false
)

var ErrInvalidParameterSize = errors.New("A parameter given has an invalid size")

// AppServer represents the app server control context
// Locking is not needed because the event loop that calls these functions
// is sequential in nature(even the reconnect method call)
type AppServer struct {
	addr       string
	user, pass string
	// Out auth JWT
	jwt  string
	conn *grpc.ClientConn

	appid, orgid, netwkid int64
	devprof               deviceProfileCache

	User          pb.UserClient
	Internal      pb.InternalClient
	Organization  pb.OrganizationClient
	Device        pb.DeviceClient
	Gateway       pb.GatewayClient
	DeviceQueue   pb.DeviceQueueClient
	DeviceProfile pb.DeviceProfileServiceClient
	Application   pb.ApplicationClient
	log           *logrus.Logger
}

func NewAppServer(address string, appID, organizationID, networkServerID int64) *AppServer {
	log := logrus.New()
	log.Level = 5
	return &AppServer{
		addr:    address,
		appid:   appID,
		orgid:   organizationID,
		netwkid: networkServerID,
		devprof: deviceProfileCache{
			profs: make(map[deviceProfileSettings]deviceProfileMeta),
		},
		log: log,
	}
}

// SetJWT simply sets the JWT for the session
func (a *AppServer) setJWT(jwt string) {
	a.jwt = jwt
}

// Login authenticates with the lora app server and obtains the JWT for
// the session
func (a *AppServer) login(username, password string) error {
	a.user = username
	a.pass = password
	loginRequest := &pb.LoginRequest{Username: a.user, Password: a.pass}

	cp, err := x509.SystemCertPool()
	if err != nil {
		log.Fatal("Failed to get system root CA certificates")
	}

	grpcDialOpts := []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
		// given the grpc-gateway is always connecting to localhost, does
		// InsecureSkipVerify=true cause any security issues?
		InsecureSkipVerify: true,
		RootCAs:            cp,
	}))}

	// Set up a connection to the server.
	conn, err := grpc.Dial(a.addr, grpcDialOpts...)
	if err != nil {
		return err
	}
	defer conn.Close()

	login := pb.NewInternalClient(conn)
	loginResponse, err := login.Login(context.Background(), loginRequest)
	if err != nil {
		return err
	}

	a.setJWT(loginResponse.Jwt)
	return nil
}

func (a *AppServer) connect() error {

	cp, err := x509.SystemCertPool()
	if err != nil {
		return errors.New("Failed to get root CA certificates")
	}

	// b, err := ioutil.ReadFile("fullchain.pem")
	// if err != nil {
	// 	log.Fatalf("read http-tls-cert cert error: %s", err)
	// }
	// cp := x509.NewCertPool()
	// if !cp.AppendCertsFromPEM(b) {
	// 	log.Fatal("failed to append certificate")
	// }

	grpcDialOpts := []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
		// given the grpc-gateway is always connecting to localhost, does
		// InsecureSkipVerify=true cause any security issues?
		InsecureSkipVerify: true,
		RootCAs:            cp,
	})), grpc.WithPerRPCCredentials(&authMeta{a.jwt})}

	// Set up a connection to the server.
	a.conn, err = grpc.Dial(a.addr, grpcDialOpts...)
	if err != nil {
		return err
	}

	a.User = pb.NewUserClient(a.conn)
	a.Internal = pb.NewInternalClient(a.conn)
	a.Organization = pb.NewOrganizationClient(a.conn)
	// a.Node = pb.NewNodeClient(a.conn)
	a.Device = pb.NewDeviceClient(a.conn)
	a.Gateway = pb.NewGatewayClient(a.conn)
	// a.DownlinkQueue = pb.NewDownlinkQueueClient(a.conn)
	a.DeviceQueue = pb.NewDeviceQueueClient(a.conn)
	a.DeviceProfile = pb.NewDeviceProfileServiceClient(a.conn)
	a.Application = pb.NewApplicationClient(a.conn)
	log.Println("Connected")
	return nil
}

func (a *AppServer) disconnect() error {
	return a.conn.Close()
}

// Login authenticates with the lora app server and obtains the JWT for
// the session
func (a *AppServer) Login(user, pass string) error {
	return a.login(user, pass)
}

// SetJWT simply sets the JWT for the session
func (a *AppServer) SetJWT(jwt string) {
	a.setJWT(jwt)
}

func (a *AppServer) Disconnect() error {
	return a.disconnect()
}

func (a *AppServer) Connect() error {
	return a.connect()
}

func (a *AppServer) ReLogin() error {
	err := a.disconnect()
	if err != nil {
		return err
	}
	err = a.login(a.user, a.pass)
	if err != nil {
		return err
	}
	return a.connect()
}

func (a *AppServer) GetUsers() {
	req := &pb.ListUserRequest{
		Limit:  requestLimit,
		Offset: 0,
	}
	users, err := a.User.List(context.Background(), req)
	if err != nil {
		log.Fatalf("Failed to get list of users: %v", err)
	}
	fmt.Printf("Total Users = %v\n", users.GetTotalCount)
	fmt.Printf("Results = %v\n", users.GetResult())
	for _, u := range users.GetResult() {
		fmt.Printf("Username: %s\n", u.Username)
	}
}

func (a *AppServer) ListDevices(AppID int64) ([]*pb.DeviceListItem, error) {
	req := &pb.ListDeviceByApplicationIDRequest{
		ApplicationID: AppID,
		Limit:         requestLimit,
		Offset:        0,
	}
	devices, err := a.Device.ListByApplicationID(context.Background(), req)
	// TODO: Need to use some intermediate type that has the application key
	// TODO: Need to fetch app keys also
	return devices.GetResult(), err
}

func (a *AppServer) GetDevice(DevEUI string) (*pb.GetDeviceResponse, error) {
	// req := &pb.GetDeviceRequest{DevEUI}
	// device, err := a.Device.Get(context.Background(), req)
	// TODO: Need to use some intermediate type that has the application key
	// TODO: Need to fetch appkey also
	// return device, err
	return nil, nil
}

func (a *AppServer) CreateNode(AppID int64, DevEUI, AppEUI, AppKey, Name, Description string) error {
	/* Example CreateNodeRequest
	applicationID:"4"
	name:"Test"
	description:"Testing"
	devEUI:"1122334455667788"
	appEUI:"1122334455667788"
	appKey:"11223344556677881122334455667788"
	useApplicationSettings: true,
	adrInterval:0
	installationMargin:0
	isABP:false
	isClassC:false
	relaxFCnt:false
	rx1DROffset:0
	rx2DR:0
	rxDelay:0
	rxWindow:"RX1"
	*/
	if !utils.IsValidHex(DevEUI, 64) {
		return ErrInvalidParameterSize
	}
	if !utils.IsValidHex(AppEUI, 64) {
		return ErrInvalidParameterSize
	}
	if !utils.IsValidHex(AppKey, 128) {
		return ErrInvalidParameterSize
	}
	fmt.Printf("Create: \"%s\" - \"%s\" - \"%s\"\n", DevEUI, AppEUI, AppKey)
	// req := &pb.CreateNodeRequest{
	// 	ApplicationID:          AppID,
	// 	DevEUI:                 DevEUI,
	// 	AppEUI:                 AppEUI,
	// 	AppKey:                 AppKey,
	// 	UseApplicationSettings: true,
	// 	Name:        Name,
	// 	Description: Description,
	// }
	// _, err := a.Node.Create(context.Background(), req)
	// return err
	return nil
}

func (a *AppServer) CreateNodeWithClass(AppID int64, DevEUI, AppEUI, AppKey, Name, Description string, IsClassC bool) error {
	/* Example CreateNodeRequest
	applicationID:"4"
	name:"Test"
	description:"Testing"
	devEUI:"1122334455667788"
	appEUI:"1122334455667788"
	appKey:"11223344556677881122334455667788"
	useApplicationSettings: true,
	adrInterval:0
	installationMargin:0
	isABP:false
	isClassC:false
	relaxFCnt:false
	rx1DROffset:0
	rx2DR:0
	rxDelay:0
	rxWindow:"RX1"
	*/
	if !utils.IsValidHex(DevEUI, 64) {
		return ErrInvalidParameterSize
	}
	if !utils.IsValidHex(AppEUI, 64) {
		return ErrInvalidParameterSize
	}
	if !utils.IsValidHex(AppKey, 128) {
		return ErrInvalidParameterSize
	}
	fmt.Printf("Create: \"%s\" - \"%s\" - \"%s\" - IsClassC=%v\n", DevEUI, AppEUI, AppKey, IsClassC)
	// req := &pb.CreateNodeRequest{
	// 	ApplicationID:          AppID,
	// 	DevEUI:                 DevEUI,
	// 	AppEUI:                 AppEUI,
	// 	AppKey:                 AppKey,
	// 	UseApplicationSettings: !IsClassC,
	// 	IsClassC:               IsClassC,
	// 	Name:                   Name,
	// 	Description:            Description,
	// }
	// _, err := a.Node.Create(context.Background(), req)
	// return err
	return nil
}

// UpdateNode was intended to update a node's info without crushing session keys
// but, I am not sure this is possible with updating all info.
// Unfortunately, there is no way to simply update one field
func (a *AppServer) UpdateNode(AppID int64, DevEUI, AppEUI, AppKey, Name, Description string, IsClassC bool) error {
	/* Example CreateNodeRequest
	applicationID:"4"
	name:"Test"
	description:"Testing"
	devEUI:"1122334455667788"
	appEUI:"1122334455667788"
	appKey:"11223344556677881122334455667788"
	useApplicationSettings: true,
	adrInterval:0
	installationMargin:0
	isABP:false
	isClassC:false
	relaxFCnt:false
	rx1DROffset:0
	rx2DR:0
	rxDelay:0
	rxWindow:"RX1"
	*/
	if !utils.IsValidHex(DevEUI, 64) {
		return ErrInvalidParameterSize
	}
	// fmt.Printf("UpdateDescription: \"%s\" - \"%s\" - \"%s\"\n", DevEUI, AppEUI, AppKey)
	// req := &pb.UpdateNodeRequest{
	// 	ApplicationID:          AppID,
	// 	DevEUI:                 DevEUI,
	// 	AppEUI:                 AppEUI,
	// 	AppKey:                 AppKey,
	// 	UseApplicationSettings: !IsClassC,
	// 	IsClassC:               IsClassC,
	// 	Name:                   Name,
	// 	Description:            Description,
	// }
	// _, err := a.Node.Update(context.Background(), req)
	// return err
	return nil
}

// DeleteNode will request to delete a node on the app server
func (a *AppServer) DeleteNode(DevEUI string) error {
	// req := &pb.DeleteNodeRequest{
	// 	DevEUI: DevEUI,
	// }
	// _, err := a.Node.Delete(context.Background(), req)
	// return err
	return nil
}

func DownlinkMessage(DevEUI string, data []byte) []byte {
	// req := pb.DownlinkQueueItem{
	// 	DevEUI:    DevEUI,
	// 	Data:      data,
	// 	Confirmed: defaultConfirmedDownlink,
	// 	FPort:     defaultFport,
	// }
	// payload, _ := json.Marshal(req)
	// return payload
	return nil
}

type UplinkMessage struct {
	Data []byte `json:"data"`
}

func UplinkMessageDecode(payload []byte) []byte {
	var msg UplinkMessage
	json.Unmarshal(payload, &msg)
	return []byte(base64.StdEncoding.EncodeToString(msg.Data))
}

/* Interfaces for gRPC Metadata */

type authMeta struct {
	jwt string
}

func (a authMeta) GetRequestMetadata(ctx context.Context, uri ...string) (map[string]string, error) {
	return map[string]string{"authorization": a.jwt}, nil
}

func (a authMeta) RequireTransportSecurity() bool {
	return false
}
