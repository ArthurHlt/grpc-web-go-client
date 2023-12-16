# Grpc-web-go-client

A grpc-web/http1.1 client for grpc in go, it is based on [grpc-web](https://github.com/grpc/grpc-web) protocol.

It supports only what is supported on [grpc-web](https://github.com/grpc/grpc-web) javascript implementation.
It means that it supports only unary calls and server one side streaming calls.

## Why in go ?

Grpc-web is a protocol that is already used in javascript and works over http/1.1 instead of http/2.
In some environnement you can't use http/2 with this implementation you could use http/1.1 and grpc-web in javascript.

## Usage

### Envoy configuration

You must have an [envoy](https://www.envoyproxy.io/) configured with grpc_web filter, you can use the following configuration:

```yaml
admin:
  address:
    socket_address:
      address: 0.0.0.0
      port_value: 9901
static_resources:
  listeners:
    - name: listener_0
      address:
        socket_address:
          address: 0.0.0.0
          port_value: 8080
      filter_chains:
        - filters:
            - name: envoy.http_connection_manager
              typed_config:
                '@type': >-
                  type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager
                codec_type: auto
                stat_prefix: ingress_http
                route_config:
                  name: local_route
                  virtual_hosts:
                    - name: local_service
                      domains:
                        - '*'
                      routes:
                        - match:
                            prefix: /
                          route:
                            cluster: echo_service
                http_filters:
                  - name: envoy.grpc_web
                    typed_config:
                      '@type': >-
                        type.googleapis.com/envoy.extensions.filters.http.grpc_web.v3.GrpcWeb
                  - name: envoy.filters.http.router
                    typed_config:
                      '@type': >-
                        type.googleapis.com/envoy.extensions.filters.http.router.v3.Router
  clusters:
    - name: echo_service
      connect_timeout: 0.25s
      type: LOGICAL_DNS
      typed_extension_protocol_options:
        envoy.extensions.upstreams.http.v3.HttpProtocolOptions:
          '@type': >-
            type.googleapis.com/envoy.extensions.upstreams.http.v3.HttpProtocolOptions
          explicit_http_config:
            http2_protocol_options: {}
      lb_policy: ROUND_ROBIN
      load_assignment:
        cluster_name: echo_service
        endpoints:
          - lb_endpoints:
              - endpoint:
                  address:
                    socket_address:
                      address: node-server
                      port_value: 9090
```

**Note:** Cors are unnecessary if you use this library has cors are only needed for browser client, but 
you might want to let them to be able to use grpc-web client in browser with javascript implementation.


## Example

```go
package main

import (
	"context"
	"fmt"
	"github.com/ArthurHlt/grpc-web-go-client"
	pb "google.golang.org/grpc/examples/features/proto/echo"
)

func main() {

	conn := grpcweb.NewHttp1ClientConn(
		"localhost:8080",
		nil, // if nil it use default http client
		// those next options are optionals
		grpcweb.WithScheme("http"),         // if not set it use https
		grpcweb.WithPerRPCCredentials(nil), // if you want to use grpc auth
	)

	// you can use it like a normal grpc client conn
	// note that all metadate you write become http headers
	echoClient := pb.NewEchoClient(conn)

	resp, err := echoClient.UnaryEcho(context.Background(), &pb.EchoRequest{Message: "hello"})
	if err != nil {
		panic(err)
	}
	fmt.Println(resp.Message)

	// and streaming only in one direction works
	stream, err := echoClient.ServerStreamingEcho(context.Background(), &pb.EchoRequest{Message: "hello"})
	if err != nil {
		panic(err)
	}
	for {
		msg, err := stream.Recv()
		if err != nil {
			panic(err)
		}
		fmt.Println(msg.Message)
	}
}
```
