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
)

// Configuration of the Proxmox Cloud Provider
type Config struct {
	InsecureSkipVerify   bool
	ApiEndpoint          string
	ApiUser              string
	ApiToken             string
	TargetPool           string
	ReferenceContainerId int
	TimeoutSeconds       int
	SshKeyFile           string // SSH key file to use for login
	ServerUser           string // Master node SSH login user
	ServerHost           string // Master node IP or Hostname
	User                 string // Worker node SSH login user
}

type ProxmoxManager struct {
	Client         *pm.Client
	TargetPool     string
	RefCtrId       int
	TimeoutSeconds int
	SshKeyFile     string
	ServerUser     string
	ServerHost     string
	User           string

	node   *pm.Node
	refCtr *pm.Container
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

	return &ProxmoxManager{
		Client:         client,
		TargetPool:     config.TargetPool,
		RefCtrId:       config.ReferenceContainerId,
		TimeoutSeconds: config.TimeoutSeconds,
		SshKeyFile:     config.SshKeyFile,
		ServerUser:     config.ServerUser,
		ServerHost:     config.ServerHost,
		User:           config.User,
	}, nil
}

// Implement proxmox interations on ProxmoxManager

func (p *ProxmoxManager) getInitialDetails(ctx context.Context) (err error) {
	if p.node == nil {
		// Get first node name
		log.Println("Getting first node")
		var nodeStatuses proxmox.NodeStatuses
		nodeStatuses, err = p.Client.Nodes(ctx)
		if err != nil {
			return
		}

		// Get the node object
		log.Printf("Getting node object for %s\n", nodeStatuses[0].Node)
		p.node, err = p.Client.Node(ctx, nodeStatuses[0].Node)
		if err != nil {
			return
		}
	}

	// Get reference container object
	if p.refCtr == nil {
		log.Printf("Geting reference container object for id %d\n", p.RefCtrId)
		p.refCtr, err = p.node.Container(ctx, p.RefCtrId)
		if err != nil {
			return
		}
	}

	return
}

func (p *ProxmoxManager) CloneToNewCt(ctx context.Context, newCtrOffset int) (ip netip.Addr, err error) {
	if err = p.getInitialDetails(ctx); err != nil {
		return
	}

	// Clone reference container. Return value is 0 when providing NewID
	newId := p.RefCtrId + newCtrOffset
	log.Printf("Cloning reference container %s to new container %d\n", p.refCtr.Name, newId)
	_, task, err := p.refCtr.Clone(ctx, &proxmox.ContainerCloneOptions{
		NewID:    newId,
		Hostname: fmt.Sprintf("%s-%d", p.refCtr.Name, newCtrOffset),
		Pool:     p.TargetPool,
	})
	if err != nil {
		return
	}

	// Wait for task to complete
	log.Println("Waiting for clone to complete")
	if err = task.WaitFor(ctx, 4*p.TimeoutSeconds); err != nil {
		return
	}

	// Get new container object
	log.Printf("Getting the new container object for %d\n", newId)
	newCtr, err := p.node.Container(ctx, newId)
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
	if err = task.WaitFor(ctx, p.TimeoutSeconds); err != nil {
		return
	}

	// Wait for IP to be assigned
	log.Printf("Waiting for IP address to be assigned for %s ", newCtr.Name)
	timeout := time.After(time.Duration(p.TimeoutSeconds) * time.Second)
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

func (p *ProxmoxManager) DeleteCt(ctx context.Context, ctrOffset int) (err error) {
	if err = p.getInitialDetails(ctx); err != nil {
		return
	}

	// Get container object
	log.Printf("Getting the container object for %d\n", p.RefCtrId+ctrOffset)
	ctr, err := p.node.Container(ctx, p.RefCtrId+ctrOffset)
	if err != nil {
		return
	}

	// Shutdown container
	task, err := ctr.Shutdown(ctx, true, p.TimeoutSeconds)
	if err != nil {
		return err
	}

	// Wait for shutdown
	log.Printf("Waiting for %s to shutdown", ctr.Name)
	if err = task.WaitFor(ctx, p.TimeoutSeconds); err != nil {
		return
	}

	// Delete container
	if task, err = ctr.Delete(ctx); err != nil {
		return
	}

	// Wait for deletion
	log.Printf("Waiting for %s to be deleted", ctr.Name)
	if err = task.WaitFor(ctx, p.TimeoutSeconds); err != nil {
		return
	}

	log.Printf("%s deleted!\n", ctr.Name)
	return
}

func (p *ProxmoxManager) JoinIpToK8s(ip netip.Addr) (err error) {
	joinCmd := k3sup.MakeJoin()

	joinCmd.Flags().Set("ssh-key", p.SshKeyFile)
	joinCmd.Flags().Set("server-user", p.ServerUser)
	joinCmd.Flags().Set("server-host", p.ServerHost)
	joinCmd.Flags().Set("user", p.User)
	joinCmd.Flags().Set("host", ip.String())

	log.Printf("Joining %v to %s\n", ip, p.ServerHost)
	if err = joinCmd.Execute(); err == nil {
		log.Println("Joined!")
	}
	return
}

type ProxmoxCloudProvider struct {
	config *ProxmoxManager
}

func newProxmoxCloudProvider(config *ProxmoxManager) *ProxmoxCloudProvider {
	return &ProxmoxCloudProvider{
		config: config,
	}
}

// cloudProvider.CloudProvider interface implementation on ProxmoxCloudProvider

// Name returns name of the cloud provider.
func (d *ProxmoxCloudProvider) Name() string {
	return cloudprovider.ProxmoxProviderName
}
