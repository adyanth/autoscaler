package proxmox

import (
	"encoding/json"
	"io"

	"github.com/luthermonson/go-proxmox"
	"k8s.io/autoscaler/cluster-autoscaler/cloudprovider"
)

// Configuration of the Proxmox Cloud Provider
type Config struct {
	InsecureSkipVerify   bool
	ApiUser              string
	ApiToken             string
	TargetPool           string
	ReferenceContainerId int
}

type ProxmoxManager struct {
	Client         *proxmox.Client
	TargetPool     string
	RefCtrId       int
	TimeoutSeconds int

	node   *proxmox.Node
	refCtr *proxmox.Container
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

	return &ProxmoxManager{
		Client:         nil,
		TargetPool:     config.TargetPool,
		RefCtrId:       config.ReferenceContainerId,
		TimeoutSeconds: con,
	}
}

// TODO: Implement proxmox interations on proxmoxManager

type ProxmoxCloudProvider struct {
	config *ProxmoxManager
}

func newProxmoxCloudProvider(config *ProxmoxManager) *ProxmoxCloudProvider {
	return &ProxmoxCloudProvider{
		config: config,
	}
}

// cloudProvider.CloudProvider interface implementation on ProxmoxCloudProvider
