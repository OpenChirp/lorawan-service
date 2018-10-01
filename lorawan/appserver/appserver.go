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

func NewAppServer(address string, appID, organizationID, networkServerID int64, log *logrus.Logger) *AppServer {
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
	fmt.Printf("Total Users = %v\n", users.GetTotalCount())
	fmt.Printf("Results = %v\n", users.GetResult())
	for _, u := range users.GetResult() {
		fmt.Printf("Username: %s\n", u.Username)
	}
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
