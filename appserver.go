package main

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"

	"errors"

	"strconv"

	"strings"

	"math"

	"encoding/json"

	"encoding/base64"

	pb "github.com/openchirp/lorawan/api"
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

// isValidHex indicates if value is a proper hex strings that can be contained
// with the given number of bits
func isValidHex(value string, bits int) bool {
	str := strings.ToLower(value)
	precZeros := true
	bitcount := 0
	for _, c := range str {
		// Ensure the rune is a HEX character
		if !strings.Contains("0123456789abcdef", string(c)) {
			return false
		}
		// Ensure that we are within the given bit size
		if precZeros {
			value, err := strconv.ParseInt(string(c), 16, 8)
			if err != nil {
				// This is unclear how this could ever error out
				fmt.Println("err on parse")
				return false
			}
			// Add in variable number of bits for first HEX char
			if value == 0 {
				continue
			} else {
				precZeros = false
			}
			bitcount += int(math.Ceil(math.Log2(float64(value + 1))))
		} else {
			// Add in a nibble
			bitcount += 4
		}
		if bitcount > bits {
			return false
		}
	}
	return true
}

type AppServer struct {
	addr       string
	user, pass string
	// Out auth JWT
	jwt  string
	conn *grpc.ClientConn

	User          pb.UserClient
	Internal      pb.InternalClient
	Organization  pb.OrganizationClient
	Node          pb.NodeClient
	Gateway       pb.GatewayClient
	DownlinkQueue pb.DownlinkQueueClient
	Application   pb.ApplicationClient
}

func NewAppServer(address string) AppServer {
	return AppServer{addr: address}
}

// Login authenticates with the lora app server and obtains the JWT for
// the session
func (a *AppServer) Login(username, password string) error {
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
	log.Printf("Got the JWT: %s\n", loginResponse.Jwt)

	a.jwt = loginResponse.Jwt
	return nil
}

// SetJWT simply sets the JWT for the session
func (a *AppServer) SetJWT(jwt string) {
	a.jwt = jwt
}

func (a *AppServer) Connect() error {

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
	a.Node = pb.NewNodeClient(a.conn)
	a.Gateway = pb.NewGatewayClient(a.conn)
	a.DownlinkQueue = pb.NewDownlinkQueueClient(a.conn)
	a.Application = pb.NewApplicationClient(a.conn)
	log.Println("Connected")
	return nil
}

func (a *AppServer) Disconnect() error {
	return a.conn.Close()
}

func (a *AppServer) ReLogin() error {
	err := a.Disconnect()
	if err != nil {
		return err
	}
	err = a.Login(a.user, a.pass)
	if err != nil {
		return err
	}
	return a.Connect()
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

func (a *AppServer) ListNodes(AppID int64) ([]*pb.GetNodeResponse, error) {
	req := &pb.ListNodeByApplicationIDRequest{
		ApplicationID: AppID,
		Limit:         requestLimit,
		Offset:        0,
	}
	nodes, err := a.Node.ListByApplicationID(context.Background(), req)
	return nodes.GetResult(), err
}

func (a *AppServer) CreateNode(AppID int64, DevEUI, AppEUI, AppKey, Description string) error {
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
	if !isValidHex(DevEUI, 64) {
		return ErrInvalidParameterSize
	}
	if !isValidHex(AppEUI, 64) {
		return ErrInvalidParameterSize
	}
	if !isValidHex(AppKey, 128) {
		return ErrInvalidParameterSize
	}
	fmt.Printf("Create: \"%s\" - \"%s\" - \"%s\"\n", DevEUI, AppEUI, AppKey)
	req := &pb.CreateNodeRequest{
		ApplicationID:          AppID,
		DevEUI:                 DevEUI,
		AppEUI:                 AppEUI,
		AppKey:                 AppKey,
		UseApplicationSettings: true,
		// Name will be populated with DevEUI
		Description: Description,
	}
	_, err := a.Node.Create(context.Background(), req)
	return err
}

// DeleteNode will request to delete a node on the app server
func (a *AppServer) DeleteNode(DevEUI string) error {
	req := &pb.DeleteNodeRequest{
		DevEUI: DevEUI,
	}
	_, err := a.Node.Delete(context.Background(), req)
	return err
}

func DownlinkMessage(DevEUI string, data []byte) []byte {
	req := pb.DownlinkQueueItem{
		DevEUI:    DevEUI,
		Data:      data,
		Confirmed: defaultConfirmedDownlink,
		FPort:     defaultFport,
	}
	payload, _ := json.Marshal(req)
	return payload
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
