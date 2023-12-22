package inventory

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"google.golang.org/grpc"

	"github.com/gorilla/mux"

	v1 "github.com/akash-network/akash-api/go/inventory/v1"
)

const (
	daemonSetLabelSelector = "app=hostfeaturediscovery"
	daemonSetNamespace     = "akash-services"
	grpcPort               = ":50051"
	nodeUpdateInterval     = 5 * time.Second // Duration after which to print the cluster state
	Added                  = "ADDED"
	Deleted                = "DELETED"
)

var instance *ConcurrentClusterData
var once sync.Once

// ConcurrentClusterData provides a concurrency-safe way to store and update cluster data.
type ConcurrentClusterData struct {
	sync.RWMutex
	cluster    *v1.Cluster
	podNodeMap map[string]int // Map of pod UID to node index in the cluster.Nodes slice
}

// NewConcurrentClusterData initializes a new instance of ConcurrentClusterData with empty cluster data.
func NewConcurrentClusterData() *ConcurrentClusterData {
	return &ConcurrentClusterData{
		cluster:    &v1.Cluster{Nodes: []v1.Node{}},
		podNodeMap: make(map[string]int),
	}
}

// UpdateNode updates or adds the node to the cluster data.
func (ccd *ConcurrentClusterData) UpdateNode(podUID string, node *v1.Node) {
	ccd.Lock()
	defer ccd.Unlock()

	if nodeIndex, ok := ccd.podNodeMap[podUID]; ok {
		// Node exists, update it
		ccd.cluster.Nodes[nodeIndex] = *node
	} else {
		// Node does not exist, add it
		ccd.cluster.Nodes = append(ccd.cluster.Nodes, *node)
		ccd.podNodeMap[podUID] = len(ccd.cluster.Nodes) - 1
	}
}

func (ccd *ConcurrentClusterData) RemoveNode(podUID string) {
	ccd.Lock()
	defer ccd.Unlock()

	if nodeIndex, ok := ccd.podNodeMap[podUID]; ok {
		// Remove the node from the slice
		ccd.cluster.Nodes = append(ccd.cluster.Nodes[:nodeIndex], ccd.cluster.Nodes[nodeIndex+1:]...)
		delete(ccd.podNodeMap, podUID) // Remove the entry from the map

		// Update the indices in the map
		for podUID, index := range ccd.podNodeMap {
			if index > nodeIndex {
				ccd.podNodeMap[podUID] = index - 1
			}
		}
	}
}

// Helper function to perform a deep copy of the Cluster struct.
func deepCopy(cluster *v1.Cluster) *v1.Cluster {
	if cluster == nil {
		return nil
	}

	if len(cluster.Nodes) == 0 {
		// Log a warning instead of returning an error
		log.Printf("Warning: Attempting to deep copy a cluster with an empty Nodes slice")
	}

	// Create a new Cluster instance
	copied := &v1.Cluster{}

	// Deep copy each field from the original Cluster to the new instance
	// Deep copy the Nodes slice
	copied.Nodes = make([]v1.Node, len(cluster.Nodes))
	for i, node := range cluster.Nodes {
		// Assuming Node is a struct, create a copy
		// If Node contains slices or maps, this process needs to be recursive
		copiedNode := node // This is a shallow copy, adjust as needed
		copied.Nodes[i] = copiedNode
	}

	return copied
}

func watchPods(clientset *kubernetes.Clientset, stopCh <-chan struct{}, clusterData *ConcurrentClusterData) error {
	errCh := make(chan error, 1) // Buffered error channel
	var wg sync.WaitGroup        // WaitGroup to track goroutines

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	watcher, err := clientset.CoreV1().Pods(daemonSetNamespace).Watch(ctx, metav1.ListOptions{
		LabelSelector: daemonSetLabelSelector,
	})

	if err != nil {
		return fmt.Errorf("error setting up Kubernetes watcher: %w", err)
	}

	defer watcher.Stop()

	for {
		select {
		case <-stopCh:
			log.Println("Stopping pod watcher")
			wg.Wait()    // Wait for all goroutines to finish
			close(errCh) // Close the error channel
			return nil
		case err := <-errCh:
			log.Printf("Error in goroutine: %v", err)
			// Additional error handling logic can be placed here
		case event, ok := <-watcher.ResultChan():
			if !ok {
				wg.Wait()    // Wait for all goroutines to finish
				close(errCh) // Close the error channel
				return fmt.Errorf("watcher channel closed unexpectedly")
			}

			pod, ok := event.Object.(*corev1.Pod)
			if !ok {
				log.Println("Unexpected type in watcher event")
				continue
			}

			switch event.Type {
			case Added:
				if pod.Status.Phase == corev1.PodRunning && pod.Status.PodIP != "" {
					wg.Add(1)
					go func() {
						defer wg.Done()
						if err := connectToGrpcStream(pod, clusterData); err != nil {
							errCh <- err
						}
					}()
				} else {
					wg.Add(1)
					go func() {
						defer wg.Done()
						if err := waitForPodReadyAndConnect(clientset, pod, clusterData); err != nil {
							errCh <- err
						}
					}()
				}
				log.Printf("Pod added: %s, UID: %s\n", pod.Name, pod.UID)
			case Deleted:
				clusterData.RemoveNode(string(pod.UID))
				log.Printf("Pod deleted: %s, UID: %s\n", pod.Name, pod.UID)
			}
		}
	}
}

// waitForPodReadyAndConnect waits for a pod to become ready before attempting to connect to its gRPC stream
func waitForPodReadyAndConnect(clientset *kubernetes.Clientset, pod *corev1.Pod, clusterData *ConcurrentClusterData) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute) // 10-minute timeout
	defer cancel()

	ticker := time.NewTicker(2 * time.Second) // Polling interval
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for pod %s to become ready", pod.Name)

		case <-ticker.C:
			currentPod, err := clientset.CoreV1().Pods(pod.Namespace).Get(ctx, pod.Name, metav1.GetOptions{})
			if err != nil {
				return fmt.Errorf("error getting pod status: %w", err)
			}

			if currentPod.Status.Phase == corev1.PodRunning && currentPod.Status.PodIP != "" {
				// Handle the error returned by connectToGrpcStream
				if err := connectToGrpcStream(currentPod, clusterData); err != nil {
					return fmt.Errorf("error connecting to gRPC stream for pod %s: %w", pod.Name, err)
				}
				return nil
			}
		}
	}
}

func connectToGrpcStream(pod *corev1.Pod, clusterData *ConcurrentClusterData) error {
	ipAddress := fmt.Sprintf("%s%s", pod.Status.PodIP, grpcPort)
	fmt.Println("Connecting to:", ipAddress)

	// Establish the gRPC connection
	conn, err := grpc.Dial(ipAddress, grpc.WithInsecure(), grpc.WithBlock())
	if err != nil {
		return fmt.Errorf("failed to connect to pod IP %s: %v", pod.Status.PodIP, err)
	}
	defer conn.Close()

	client := v1.NewMsgClient(conn)

	// Create a stream to receive updates from the node
	stream, err := client.QueryNode(context.Background(), &v1.VoidNoParam{})
	if err != nil {
		return fmt.Errorf("could not query node for pod IP %s: %v", pod.Status.PodIP, err)
	}

	for {
		node, err := stream.Recv()
		if err != nil {
			// Handle stream error and remove the node
			clusterData.RemoveNode(string(pod.UID))
			return fmt.Errorf("stream closed for pod UID %s: %v", pod.UID, err)
		}

		// Update the node information in the cluster data
		clusterData.UpdateNode(string(pod.UID), node)
	}
}

func printCluster() {
	// Retrieve a deep copy of the current cluster state
	cluster := GetCurrentClusterState()

	// If no nodes to print, just return
	if len(cluster.Nodes) == 0 {
		fmt.Println("No nodes in the cluster.")
		return
	}

	// Print the cluster state
	jsonCluster, err := json.Marshal(cluster)
	if err != nil {
		log.Fatalf("error marshaling cluster struct into JSON: %v", err)
	}

	fmt.Println(string(jsonCluster))
}

func FeatureDiscovery(ctx context.Context) error {
	fmt.Println("Starting up gRPC client...")

	// Use in-cluster configuration
	config, err := rest.InClusterConfig()
	if err != nil {
		return fmt.Errorf("error obtaining in-cluster config: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("error creating Kubernetes client: %v", err)
	}

	clusterData := GetInstance()

	var wg sync.WaitGroup

	// Start the watcher in a goroutine with error handling
	errCh := make(chan error, 1)
	stopCh := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(errCh)
		if err := watchPods(clientset, stopCh, clusterData); err != nil {
			errCh <- err
		}
	}()

	// Error handling goroutine
	go func() {
		for err := range errCh {
			// Log errors but don't exit
			log.Printf("Error from watchPods: %v", err)
		}
	}()

	// Start a ticker to periodically check/print the cluster state
	ticker := time.NewTicker(nodeUpdateInterval)
	go func() {
		for {
			select {
			case <-ticker.C:
				printCluster()
			case <-ctx.Done():
				// Context canceled, cleanup and exit
				ticker.Stop()
				return
			}
		}
	}()

	// API endpoint which serves feature discovery data to Akash Provider
	router := mux.NewRouter()
	router.HandleFunc("/getClusterState", getClusterStateHandler).Methods("GET")

	// Use a separate goroutine for HTTP server
	httpErrCh := make(chan error, 1)
	go func() {
		httpErrCh <- http.ListenAndServe(":8081", router)
	}()

	// Wait for all goroutines to finish or for context cancellation
	select {
	case err := <-httpErrCh:
		return fmt.Errorf("HTTP server error: %v", err)
	case err := <-errCh:
		return err
	case <-ctx.Done():
		close(stopCh)
		wg.Wait()
		return ctx.Err()
	}
}

// GetInstance returns the singleton instance of ConcurrentClusterData.
func GetInstance() *ConcurrentClusterData {
	once.Do(func() {
		log.Println("Initializing ConcurrentClusterData instance")
		instance = &ConcurrentClusterData{
			cluster:    &v1.Cluster{Nodes: []v1.Node{}},
			podNodeMap: make(map[string]int),
		}
	})
	return instance
}

// GetCurrentClusterState returns a deep copy of the current state of the cluster and is used primarily for API GET data
func GetCurrentClusterState() *v1.Cluster {
	// Use the singleton instance to get the cluster
	clusterData := GetInstance()

	// Return a deep copy of the cluster
	return deepCopy(clusterData.cluster)
}

func getClusterStateHandler(w http.ResponseWriter, r *http.Request) {
	clusterState := GetCurrentClusterState()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(clusterState); err != nil {
		// Log the error
		log.Printf("Error encoding response: %v", err)

		// Write an error response
		http.Error(w, fmt.Sprintf("{\"error\": \"Internal Server Error: %v\"}", err), http.StatusInternalServerError)
		return
	}
}
