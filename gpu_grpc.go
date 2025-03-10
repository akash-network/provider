package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

func main() {
	// Check if the socket exists
	socketPath := "/var/lib/kubelet/device-plugins/nvidia-gpu.sock"
	if _, err := os.Stat(socketPath); os.IsNotExist(err) {
		log.Fatalf("Socket not found at %s", socketPath)
	}

	// Create a gRPC connection
	conn, err := grpc.Dial(
		socketPath,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDialer(func(addr string, timeout time.Duration) (net.Conn, error) {
			return net.DialTimeout("unix", addr, timeout)
		}),
	)
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	// Create a device plugin client
	client := pluginapi.NewDevicePluginClient(conn)

	// Create a context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// List available devices
	resp, err := client.ListAndWatch(ctx, &pluginapi.Empty{})
	if err != nil {
		log.Fatalf("Failed to list devices: %v", err)
	}

	// Print device information
	for {
		response, err := resp.Recv()
		if err != nil {
			log.Printf("Error receiving device list: %v", err)
			break
		}
		fmt.Printf("Device List Response:\n")
		for _, device := range response.Devices {
			fmt.Printf("  Device ID: %s\n", device.ID)
			fmt.Printf("  Health: %s\n", device.Health)
			fmt.Printf("  Topology: %v\n", device.Topology)
		}
	}
} 