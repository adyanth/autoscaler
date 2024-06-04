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
	"strconv"
	"strings"
	"time"

	k3sup "github.com/alexellis/k3sup/cmd"
	pm "github.com/luthermonson/go-proxmox"
	apiv1 "k8s.io/api/core/v1"
	"k8s.io/autoscaler/cluster-autoscaler/cloudprovider"
	"k8s.io/autoscaler/cluster-autoscaler/config"
)

const (
	ManagedByTag = "mgd-by-pxmx-ca"
	ProviderId   = "proxmox"
	GPULabel     = "proxmox/gpu"
)

type NodeConfig struct {
	RefCtrId           int
	TargetPool         string
	WorkerNamePrefix   string
	MinSize            int
	MaxSize            int
	AutoScalingOptions *config.NodeGroupAutoscalingOptions
}

type K3sConfig struct {
	SshKeyFile string // SSH key file to use for login
	ServerUser string // Master node SSH login user
	ServerHost string // Master node IP or Hostname
	User       string // Worker node SSH login user
}

type ProxmoxConfig struct {
	ApiEndpoint        string
	ApiUser            string
	ApiToken           string
	InsecureSkipVerify bool
	TimeoutSeconds     int
}

// Configuration of the Proxmox Cloud Provider
type Config struct {
	ProxmoxConfig *ProxmoxConfig
	NodeConfigs   []*NodeConfig
	K3sConfig     *K3sConfig
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

	doNotUseNodeLabel bool
}

type ProxmoxManager struct {
	Client            *pm.Client
	NodeGroupManagers map[string]*NodeGroupManager

	doNotUseNodeLabel bool
}

func newProxmoxManager(configFileReader io.ReadCloser) (proxmox *ProxmoxManager, err error) {
	// Sometimes the node info does not have the label map populated, so cannot use it.
	// Is this a bug?
	doNotUseNodeLabel := true

	data, err := io.ReadAll(configFileReader)
	if err != nil {
		return
	}

	config := Config{}
	if err = json.Unmarshal(data, &config); err != nil {
		return
	}

	if config.ProxmoxConfig == nil {
		return nil, fmt.Errorf("proxmoxConfig cannot be empty")
	}
	if config.K3sConfig == nil {
		return nil, fmt.Errorf("k3sConfig cannot be empty")
	}
	if len(config.NodeConfigs) == 0 {
		return nil, fmt.Errorf("need at least one entry nodeConfigs")
	}

	httpClient := http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: config.ProxmoxConfig.InsecureSkipVerify,
			},
		},
	}

	client := pm.NewClient(config.ProxmoxConfig.ApiEndpoint,
		pm.WithHTTPClient(&httpClient),
		pm.WithAPIToken(config.ProxmoxConfig.ApiUser, config.ProxmoxConfig.ApiToken),
	)

	nodeGroupManagers := make(map[string]*NodeGroupManager, len(config.NodeConfigs))

	for _, nc := range config.NodeConfigs {
		nodeGroupManagers[nc.TargetPool] = &NodeGroupManager{
			Client:         client,
			NodeConfig:     nc,
			K3sConfig:      config.K3sConfig,
			TimeoutSeconds: config.ProxmoxConfig.TimeoutSeconds,

			currentSize:       0,
			targetSize:        0,
			doNotUseNodeLabel: doNotUseNodeLabel,
		}
	}

	proxmoxManager := &ProxmoxManager{
		Client:            client,
		NodeGroupManagers: nodeGroupManagers,

		doNotUseNodeLabel: doNotUseNodeLabel,
	}

	if err = proxmoxManager.getInitialDetails(context.Background()); err != nil {
		return
	}

	return proxmoxManager, nil
}

// Implement proxmox interations on ProxmoxManager

func (p *ProxmoxManager) getInitialDetails(ctx context.Context) (err error) {
	// Get first node name
	log.Println("Getting first node")
	var nodeStatuses pm.NodeStatuses
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
		if err = ngm.fillCurrentSize(ctx); err != nil {
			return
		}
		ngm.targetSize = ngm.currentSize

		// Create at least one node, until TemplateNodeInfo is implemented on the NodeGroupManager
		if ngm.currentSize == 0 {
			if err := ngm.IncreaseSize(1); err != nil {
				return err
			}
		}
	}

	return
}

func (n *NodeGroupManager) getTagsForOffset(offset int) string {
	tags := make([]string, 0, 3)
	tags = append(tags, ManagedByTag)
	tags = append(tags, fmt.Sprintf("refId-%d", n.NodeConfig.RefCtrId))
	tags = append(tags, fmt.Sprintf("offset-%d", offset))
	return strings.Join(tags, ";")
}

func (n *NodeGroupManager) getTagsFromTagString(tags string) []string {
	return strings.Split(tags, ";")
}

func (n *NodeGroupManager) cloneToNewCt(ctx context.Context, newCtrOffset int) (ip netip.Addr, err error) {
	// Clone reference container. Return value is 0 when providing NewID
	newId := n.NodeConfig.RefCtrId + newCtrOffset
	log.Printf("Cloning reference container %s to new container %d\n", n.refCtr.Name, newId)
	_, task, err := n.refCtr.Clone(ctx, &pm.ContainerCloneOptions{
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

	// Add needed tags
	tags := n.getTagsForOffset(newId)
	log.Printf("Adding tags to the new container %s: %s\n", newCtr.Name, tags)
	task, err = newCtr.Config(ctx, pm.ContainerOption{
		Name:  "tags",
		Value: tags,
	})
	if err != nil {
		return
	}
	if err = task.WaitFor(ctx, n.TimeoutSeconds); err != nil {
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

func (n *NodeGroupManager) getCtrIdFromOffset(offset int) int {
	return n.NodeConfig.RefCtrId + offset
}

func (n *NodeGroupManager) DeleteCt(ctx context.Context, ctrOffset int) (err error) {
	// Get container object
	log.Printf("Getting the container object for %d\n", n.getCtrIdFromOffset(ctrOffset))
	ctr, err := n.node.Container(ctx, n.getCtrIdFromOffset(ctrOffset))
	if err != nil {
		return
	}

	// Shutdown container
	if ctr.Status == "running" {
		task, err := ctr.Shutdown(ctx, true, n.TimeoutSeconds)
		if err != nil {
			return err
		}

		// Wait for shutdown
		log.Printf("Waiting for %s to shutdown", ctr.Name)
		if err = task.WaitFor(ctx, n.TimeoutSeconds); err != nil {
			return err
		}
	}

	// Delete container
	task, err := ctr.Delete(ctx)
	if err != nil {
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

func extractDetailsFromProviderId(node *apiv1.Node) (targetPool string, refCtrId int, offset int, err error) {
	providerAndRest := strings.Split(node.Spec.ProviderID, "://")

	// Check if managed by provider
	if len(providerAndRest) != 2 || providerAndRest[0] != ProviderId {
		return
	}

	targelPoolRefCtrIdOffset := strings.Split(providerAndRest[1], "/")
	if len(targelPoolRefCtrIdOffset) != 3 {
		err = fmt.Errorf("node %s providerId invalid, does not match %s provider format: %s", node.Name, ProviderId, node.Spec.ProviderID)
		return
	}

	targetPool = targelPoolRefCtrIdOffset[0]
	refCtrId, errConvId := strconv.Atoi(targelPoolRefCtrIdOffset[1])
	offset, errConvOff := strconv.Atoi(targelPoolRefCtrIdOffset[2])
	if errConvId != nil || errConvOff != nil {
		err = fmt.Errorf("node %s providerId invalid, does not match %s provider format: %s", node.Name, ProviderId, node.Spec.ProviderID)
		return
	}
	return
}

func (p *ProxmoxManager) getDetailsFromNode(node *apiv1.Node) (n *NodeGroupManager, offset int, err error) {
	targetPool, refCtrId, offset, err := extractDetailsFromProviderId(node)
	if err != nil {
		return
	}

	// Not managed by us
	if targetPool == "" && refCtrId == 0 && offset == 0 {
		return
	}

	var ok bool
	if n, ok = p.NodeGroupManagers[targetPool]; !ok || n.NodeConfig.RefCtrId != refCtrId {
		return nil, 0, fmt.Errorf("no node group managers found for node: %s with pool: %s and refCtrId: %d", node.Name, targetPool, refCtrId)
	}

	return
}

func (n *NodeGroupManager) getDetailsFromNode(node *apiv1.Node) (offset int, err error) {
	targetPool, refCtrId, offset, err := extractDetailsFromProviderId(node)
	if err != nil {
		return
	}

	if n.NodeConfig.TargetPool != targetPool || n.NodeConfig.RefCtrId != refCtrId {
		return 0, fmt.Errorf("this nodegroup manager does not contain node: %s with pool: %s and refCtrId: %d", node.Name, targetPool, refCtrId)
	}

	return
}

func (n *NodeGroupManager) getProviderId(offset int) string {
	return fmt.Sprintf("%s://%s/%d/%d", ProviderId, n.NodeConfig.TargetPool, n.NodeConfig.RefCtrId, offset)
}

func (n *NodeGroupManager) joinIpToK8s(ip netip.Addr, offset int) (err error) {
	joinCmd := k3sup.MakeJoin()

	joinCmd.Flags().Set("ssh-key", n.K3sConfig.SshKeyFile)
	joinCmd.Flags().Set("server-user", n.K3sConfig.ServerUser)
	joinCmd.Flags().Set("server-host", n.K3sConfig.ServerHost)
	joinCmd.Flags().Set("user", n.K3sConfig.User)
	joinCmd.Flags().Set("host", ip.String())
	joinCmd.Flags().Set("k3s-extra-args", fmt.Sprintf("--provider-id %s", n.getProviderId(offset)))

	log.Printf("Joining %v to %s\n", ip, n.K3sConfig.ServerHost)
	if err = joinCmd.Execute(); err == nil {
		log.Println("Joined!")
	}
	return
}

func (n *NodeGroupManager) CreateK3sWorker(ctx context.Context, newCtrOffset int) (err error) {
	if ip, err := n.cloneToNewCt(ctx, newCtrOffset); err != nil {
		return err
	} else {
		return n.joinIpToK8s(ip, newCtrOffset)
	}
}

func (n *NodeGroupManager) OwnedNodeOffset(node *apiv1.Node) (nodeOffset int, err error) {
	return n.getDetailsFromNode(node)
}

func (n *NodeGroupManager) OwnedNode(node *apiv1.Node) bool {
	_, err := n.getDetailsFromNode(node)
	return err == nil
}

func (m *ProxmoxManager) OwnedNode(node *apiv1.Node) bool {
	return strings.HasPrefix(node.Spec.ProviderID, "proxmox://")
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
