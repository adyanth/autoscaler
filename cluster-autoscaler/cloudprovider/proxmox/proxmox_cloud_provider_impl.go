package proxmox

import (
	apiv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/autoscaler/cluster-autoscaler/cloudprovider"
	"k8s.io/autoscaler/cluster-autoscaler/utils/errors"
)

// cloudProvider.CloudProvider interface implementation on ProxmoxCloudProvider

// Name returns name of the cloud provider.
func (p *ProxmoxCloudProvider) Name() string {
	return cloudprovider.ProxmoxProviderName
}

// NodeGroups returns all node groups configured for this cloud provider.
func (p *ProxmoxCloudProvider) NodeGroups() []cloudprovider.NodeGroup {}

// NodeGroupForNode returns the node group for the given node, nil if the node
// should not be processed by cluster autoscaler, or non-nil error if such
// occurred. Must be implemented.
func (p *ProxmoxCloudProvider) NodeGroupForNode(*apiv1.Node) (cloudprovider.NodeGroup, error) {}

// HasInstance returns whether the node has corresponding instance in cloud provider,
// true if the node has an instance, false if it no longer exists
func (p *ProxmoxCloudProvider) HasInstance(*apiv1.Node) (bool, error) {}

// Pricing returns pricing model for this cloud provider or error if not available.
// Implementation optional.
func (p *ProxmoxCloudProvider) Pricing() (cloudprovider.PricingModel, errors.AutoscalerError) {}

// GetAvailableMachineTypes get all machine types that can be requested from the cloud provider.
// Implementation optional.
func (p *ProxmoxCloudProvider) GetAvailableMachineTypes() ([]string, error) {}

// NewNodeGroup builds a theoretical node group based on the node definition provided. The node group is not automatically
// created on the cloud provider side. The node group is not returned by NodeGroups() until it is created.
// Implementation optional.
func (p *ProxmoxCloudProvider) NewNodeGroup(machineType string, labels map[string]string, systemLabels map[string]string,
	taints []apiv1.Taint, extraResources map[string]resource.Quantity) (cloudprovider.NodeGroup, error) {
}

// GetResourceLimiter returns struct containing limits (max, min) for resources (cores, memory etc.).
func (p *ProxmoxCloudProvider) GetResourceLimiter() (*cloudprovider.ResourceLimiter, error) {}

// GPULabel returns the label added to nodes with GPU resource.
func (p *ProxmoxCloudProvider) GPULabel() string {}

// GetAvailableGPUTypes return all available GPU types cloud provider supports.
func (p *ProxmoxCloudProvider) GetAvailableGPUTypes() map[string]struct{} {}

// GetNodeGpuConfig returns the label, type and resource name for the GPU added to node. If node doesn't have
// any GPUs, it returns nil.
func (p *ProxmoxCloudProvider) GetNodeGpuConfig(*apiv1.Node) *cloudprovider.GpuConfig {}

// Cleanup cleans up open resources before the cloud provider is destroyed, i.e. go routines etc.
func (p *ProxmoxCloudProvider) Cleanup() error {}

// Refresh is called before every main loop and can be used to dynamically update cloud provider state.
// In particular the list of node groups returned by NodeGroups can change as a result of CloudProvider.Refresh().
func (p *ProxmoxCloudProvider) Refresh() error {}
