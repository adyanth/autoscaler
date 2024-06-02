package proxmox

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/netip"
	"time"

	k3sup "github.com/alexellis/k3sup/cmd"
	"github.com/luthermonson/go-proxmox"
	pm "github.com/luthermonson/go-proxmox"
	"k8s.io/autoscaler/cluster-autoscaler/cloudprovider"
	"k8s.io/autoscaler/cluster-autoscaler/config"
)

type NodeConfig struct {
	RefCtrId           int
	TargetPool         string
	WorkerNamePrefix   string
	MinSize            int
	MaxSize            int
	AutoScalingOptions config.NodeGroupAutoscalingOptions
}

type K3sConfig struct {
	SshKeyFile string // SSH key file to use for login
	ServerUser string // Master node SSH login user
	ServerHost string // Master node IP or Hostname
	User       string // Worker node SSH login user
}

// Configuration of the Proxmox Cloud Provider
type Config struct {
	InsecureSkipVerify bool
	ApiEndpoint        string
	ApiUser            string
	ApiToken           string
	NodeConfigs        []NodeConfig
	TimeoutSeconds     int
	K3sConfig          K3sConfig
}

type NodeGroupManager struct {
	Client         *pm.Client
	NodeConfig     *NodeConfig
	K3sConfig      *K3sConfig
	TimeoutSeconds int

	node        *pm.Node
	refCtr      *pm.Container
	currentSize int
	targetSize  int
}

type ProxmoxManager struct {
	Client            *pm.Client
	NodeGroupManagers []*NodeGroupManager
}

func newProxmoxManager(configFileReader io.ReadCloser) (proxmox *ProxmoxManager, err error) {
	data, err := io.ReadAll(configFileReader)
	if err != nil {
		return
	}

	config := Config{}
	if err = json.Unmarshal(data, config); err != nil {
		return
	}

	httpClient := http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: config.InsecureSkipVerify,
			},
		},
	}

	client := pm.NewClient(config.ApiEndpoint,
		pm.WithHTTPClient(&httpClient),
		pm.WithAPIToken(config.ApiUser, config.ApiToken),
	)

	nodeGroupManagers := make([]*NodeGroupManager, 0, len(config.NodeConfigs))

	for _, nc := range config.NodeConfigs {
		nodeGroupManagers = append(nodeGroupManagers, &NodeGroupManager{
			Client:         client,
			NodeConfig:     &nc,
			K3sConfig:      &config.K3sConfig,
			TimeoutSeconds: config.TimeoutSeconds,

			currentSize: 0,
			targetSize:  0,
		})
	}

	proxmoxManager := &ProxmoxManager{
		Client:            client,
		NodeGroupManagers: nodeGroupManagers,
	}

	proxmoxManager.getInitialDetails(context.Background())

	return proxmoxManager, nil
}

// Implement proxmox interations on ProxmoxManager

func (p *ProxmoxManager) getInitialDetails(ctx context.Context) (err error) {
	// Get first node name
	log.Println("Getting first node")
	var nodeStatuses proxmox.NodeStatuses
	nodeStatuses, err = p.Client.Nodes(ctx)
	if err != nil {
		return
	}

	// Get the node object
	log.Printf("Getting node object for %s\n", nodeStatuses[0].Node)
	node, err := p.Client.Node(ctx, nodeStatuses[0].Node)
	if err != nil {
		return
	}

	for _, ngm := range p.NodeGroupManagers {
		ngm.node = node

		// Get reference container object
		if ngm.refCtr == nil {
			log.Printf("Geting reference container object for id %d\n", ngm.NodeConfig.RefCtrId)
			ngm.refCtr, err = ngm.node.Container(ctx, ngm.NodeConfig.RefCtrId)
			if err != nil {
				return
			}
		}

		// Default name from template name
		if ngm.NodeConfig.WorkerNamePrefix == "" {
			ngm.NodeConfig.WorkerNamePrefix = ngm.refCtr.Name
		}

		// Set current and target size
		if err = ngm.FillCurrentSize(ctx); err != nil {
			return
		}
		ngm.targetSize = ngm.currentSize
	}

	return
}

func (n *NodeGroupManager) CloneToNewCt(ctx context.Context, newCtrOffset int) (ip netip.Addr, err error) {
	// Clone reference container. Return value is 0 when providing NewID
	newId := n.NodeConfig.RefCtrId + newCtrOffset
	log.Printf("Cloning reference container %s to new container %d\n", n.refCtr.Name, newId)
	_, task, err := n.refCtr.Clone(ctx, &proxmox.ContainerCloneOptions{
		NewID:    newId,
		Hostname: fmt.Sprintf("%s-%d", n.NodeConfig.WorkerNamePrefix, newCtrOffset),
		Pool:     n.NodeConfig.TargetPool,
	})
	if err != nil {
		return
	}

	// Wait for task to complete
	log.Println("Waiting for clone to complete")
	if err = task.WaitFor(ctx, 4*n.TimeoutSeconds); err != nil {
		return
	}

	// Get new container object
	log.Printf("Getting the new container object for %d\n", newId)
	newCtr, err := n.node.Container(ctx, newId)
	if err != nil {
		return
	}

	// Start the new container
	log.Printf("Starting the new container %s\n", newCtr.Name)
	task, err = newCtr.Start(ctx)
	if err != nil {
		return
	}

	// Wait for start up
	log.Printf("Waiting for %s to start up", newCtr.Name)
	if err = task.WaitFor(ctx, n.TimeoutSeconds); err != nil {
		return
	}

	// Wait for IP to be assigned
	log.Printf("Waiting for IP address to be assigned for %s ", newCtr.Name)
	timeout := time.After(time.Duration(n.TimeoutSeconds) * time.Second)
	for {
		fmt.Print(".")
		select {
		case <-timeout:
			return netip.Addr{}, errors.New("timed out waiting for IP")
		default:
			// Get list of ifaces
			ifaces, err := newCtr.Interfaces(ctx)
			if err != nil {
				return netip.Addr{}, err
			}

			// Check the 2nd iface (1st is loopback)
			if len(ifaces) >= 2 && len(ifaces[1].Inet) > 0 {
				// Convert string to IP and return
				prefix, err := netip.ParsePrefix(ifaces[1].Inet)
				if err != nil {
					return netip.Addr{}, err
				}
				fmt.Println()
				log.Printf("Container %s created with IP %v\n", newCtr.Name, prefix)
				return prefix.Addr(), nil
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func (n *NodeGroupManager) DeleteCt(ctx context.Context, ctrOffset int) (err error) {
	// Get container object
	log.Printf("Getting the container object for %d\n", n.NodeConfig.RefCtrId+ctrOffset)
	ctr, err := n.node.Container(ctx, n.NodeConfig.RefCtrId+ctrOffset)
	if err != nil {
		return
	}

	// Shutdown container
	task, err := ctr.Shutdown(ctx, true, n.TimeoutSeconds)
	if err != nil {
		return err
	}

	// Wait for shutdown
	log.Printf("Waiting for %s to shutdown", ctr.Name)
	if err = task.WaitFor(ctx, n.TimeoutSeconds); err != nil {
		return
	}

	// Delete container
	if task, err = ctr.Delete(ctx); err != nil {
		return
	}

	// Wait for deletion
	log.Printf("Waiting for %s to be deleted", ctr.Name)
	if err = task.WaitFor(ctx, n.TimeoutSeconds); err != nil {
		return
	}

	log.Printf("%s deleted!\n", ctr.Name)
	return
}

func (n *NodeGroupManager) JoinIpToK8s(ip netip.Addr) (err error) {
	joinCmd := k3sup.MakeJoin()

	joinCmd.Flags().Set("ssh-key", n.K3sConfig.SshKeyFile)
	joinCmd.Flags().Set("server-user", n.K3sConfig.ServerUser)
	joinCmd.Flags().Set("server-host", n.K3sConfig.ServerHost)
	joinCmd.Flags().Set("user", n.K3sConfig.User)
	joinCmd.Flags().Set("host", ip.String())

	log.Printf("Joining %v to %s\n", ip, n.K3sConfig.ServerHost)
	if err = joinCmd.Execute(); err == nil {
		log.Println("Joined!")
	}
	return
}

func (n *NodeGroupManager) CreateK3sWorker(ctx context.Context, newCtrOffset int) (err error) {
	if ip, err := n.CloneToNewCt(ctx, newCtrOffset); err != nil {
		return err
	} else {
		return n.JoinIpToK8s(ip)
	}
}

type ProxmoxCloudProvider struct {
	manager         *ProxmoxManager
	resourceLimiter *cloudprovider.ResourceLimiter
}

func newProxmoxCloudProvider(manager *ProxmoxManager, rl *cloudprovider.ResourceLimiter) *ProxmoxCloudProvider {
	return &ProxmoxCloudProvider{
		manager:         manager,
		resourceLimiter: rl,
	}
}
