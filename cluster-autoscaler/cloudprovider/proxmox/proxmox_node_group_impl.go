package proxmox

import (
	apiv1 "k8s.io/api/core/v1"
	"k8s.io/autoscaler/cluster-autoscaler/cloudprovider"
	"k8s.io/autoscaler/cluster-autoscaler/config"
	schedulerframework "k8s.io/kubernetes/pkg/scheduler/framework"
)

// MaxSize returns maximum size of the node group.
func (n *NodeGroupManager) MaxSize() int {}

// MinSize returns minimum size of the node group.
func (n *NodeGroupManager) MinSize() int {}

// TargetSize returns the current target size of the node group. It is possible that the
// number of nodes in Kubernetes is different at the moment but should be equal
// to Size() once everything stabilizes (new nodes finish startup and registration or
// removed nodes are deleted completely). Implementation required.
func (n *NodeGroupManager) TargetSize() (int, error) {}

// IncreaseSize increases the size of the node group. To delete a node you need
// to explicitly name it and use DeleteNode. This function should wait until
// node group size is updated. Implementation required.
func (n *NodeGroupManager) IncreaseSize(delta int) error {}

// AtomicIncreaseSize tries to increase the size of the node group atomically.
//   - If the method returns nil, it guarantees that delta instances will be added to the node group
//     within its MaxNodeProvisionTime. The function should wait until node group size is updated.
//     The cloud provider is responsible for tracking and ensuring successful scale up asynchronously.
//   - If the method returns an error, it guarantees that no new instances will be added to the node group
//     as a result of this call. The cloud provider is responsible for ensuring that before returning from the method.
//
// Implementation is optional. If implemented, CA will take advantage of the method while scaling up
// GenericScaleUp ProvisioningClass, guaranteeing that all instances required for such a ProvisioningRequest
// are provisioned atomically.
func (n *NodeGroupManager) AtomicIncreaseSize(delta int) error {}

// DeleteNodes deletes nodes from this node group. Error is returned either on
// failure or if the given node doesn't belong to this node group. This function
// should wait until node group size is updated. Implementation required.
func (n *NodeGroupManager) DeleteNodes([]*apiv1.Node) error {}

// DecreaseTargetSize decreases the target size of the node group. This function
// doesn't permit to delete any existing node and can be used only to reduce the
// request for new nodes that have not been yet fulfilled. Delta should be negative.
// It is assumed that cloud provider will not delete the existing nodes when there
// is an option to just decrease the target. Implementation required.
func (n *NodeGroupManager) DecreaseTargetSize(delta int) error {}

// Id returns an unique identifier of the node group.
func (n *NodeGroupManager) Id() string {}

// Debug returns a string containing all information regarding this node group.
func (n *NodeGroupManager) Debug() string {}

// Nodes returns a list of all nodes that belong to this node group.
// It is required that Instance objects returned by this method have Id field set.
// Other fields are optional.
// This list should include also instances that might have not become a kubernetes node yet.
func (n *NodeGroupManager) Nodes() ([]cloudprovider.Instance, error) {}

// TemplateNodeInfo returns a schedulerframework.NodeInfo structure of an empty
// (as if just started) node. This will be used in scale-up simulations to
// predict what would a new node look like if a node group was expanded. The returned
// NodeInfo is expected to have a fully populated Node object, with all of the labels,
// capacity and allocatable information as well as all pods that are started on
// the node by default, using manifest (most likely only kube-proxy). Implementation optional.
func (n *NodeGroupManager) TemplateNodeInfo() (*schedulerframework.NodeInfo, error) {}

// Exist checks if the node group really exists on the cloud provider side. Allows to tell the
// theoretical node group from the real one. Implementation required.
func (n *NodeGroupManager) Exist() bool {}

// Create creates the node group on the cloud provider side. Implementation optional.
func (n *NodeGroupManager) Create() (cloudprovider.NodeGroup, error) {}

// Delete deletes the node group on the cloud provider side.
// This will be executed only for autoprovisioned node groups, once their size drops to 0.
// Implementation optional.
func (n *NodeGroupManager) Delete() error {}

// Autoprovisioned returns true if the node group is autoprovisioned. An autoprovisioned group
// was created by CA and can be deleted when scaled to 0.
func (n *NodeGroupManager) Autoprovisioned() bool {}

// GetOptions returns NodeGroupAutoscalingOptions that should be used for this particular
// NodeGroup. Returning a nil will result in using default options.
// Implementation optional. Callers MUST handle `cloudprovider.ErrNotImplemented`.
func (n *NodeGroupManager) GetOptions(defaults config.NodeGroupAutoscalingOptions) (*config.NodeGroupAutoscalingOptions, error) {
}
