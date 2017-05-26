package main

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"

	pb "github.com/openchirp/lorawan/api"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

type AppServer struct {
	addr string
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
func (a *AppServer) Login(username, password string) {
	loginRequest := &pb.LoginRequest{Username: username, Password: password}

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
		log.Fatalf("Could not connect to app server to login: %v\n", err)
	}
	defer conn.Close()

	login := pb.NewInternalClient(conn)
	loginResponse, err := login.Login(context.Background(), loginRequest)
	if err != nil {
		log.Fatalf("Failed to issue the login RPC: %v\n", err)
	}
	log.Printf("Got the JWT: %s\n", loginResponse.Jwt)

	a.jwt = loginResponse.Jwt
}

// SetJWT simply sets the JWT for the session
func (a *AppServer) SetJWT(jwt string) {
	a.jwt = jwt
}

func (a *AppServer) Connect() {

	cp, err := x509.SystemCertPool()
	if err != nil {
		log.Fatal("Failed to get root CA certificates")
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
	a.conn, err = grpc.Dial(address, grpcDialOpts...)
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}

	a.User = pb.NewUserClient(a.conn)
	a.Internal = pb.NewInternalClient(a.conn)
	a.Organization = pb.NewOrganizationClient(a.conn)
	a.Node = pb.NewNodeClient(a.conn)
	a.Gateway = pb.NewGatewayClient(a.conn)
	a.DownlinkQueue = pb.NewDownlinkQueueClient(a.conn)
	a.Application = pb.NewApplicationClient(a.conn)
	log.Println("Connected")
}

func (a *AppServer) Disconnect() error {
	return a.conn.Close()
}

func (a *AppServer) GetUsers() {
	users, err := a.User.List(context.Background(), &pb.ListUserRequest{Limit: 2000000000, Offset: 0})
	if err != nil {
		log.Fatalf("Failed to get list of users: %v", err)
	}
	fmt.Printf("Total Users = %v\n", users.GetTotalCount)
	fmt.Printf("Results = %v\n", users.GetResult())
	for _, u := range users.GetResult() {
		fmt.Printf("Username: %s\n", u.Username)
	}
}

func (a *AppServer) Add() {
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
