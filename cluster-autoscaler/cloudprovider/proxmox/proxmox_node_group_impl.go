package proxmox

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	pm "github.com/luthermonson/go-proxmox"
	apiv1 "k8s.io/api/core/v1"
	"k8s.io/autoscaler/cluster-autoscaler/cloudprovider"
	"k8s.io/autoscaler/cluster-autoscaler/config"
	schedulerframework "k8s.io/kubernetes/pkg/scheduler/framework"
)

func (n *NodeGroupManager) GetPoolNodes(ctx context.Context) ([]*pm.ClusterResource, error) {
	// List existing LXC containers
	pool, err := n.Client.Pool(ctx, n.NodeConfig.TargetPool, "lxc")
	if err != nil {
		return nil, err
	}

	nodes := make([]*pm.ClusterResource, len(pool.Members))
	for _, ctr := range pool.Members {
		if strings.HasPrefix(ctr.Name, fmt.Sprintf("%s-", n.NodeConfig.WorkerNamePrefix)) {
			nodes = append(nodes, &ctr)
		}
	}

	return nodes, nil
}

func (n *NodeGroupManager) FillCurrentSize(ctx context.Context) error {
	if resources, err := n.GetPoolNodes(ctx); err != nil {
		return err
	} else {
		n.currentSize = len(resources)
	}
	return nil
}

// MaxSize returns maximum size of the node group.
func (n *NodeGroupManager) MaxSize() int {
	return n.NodeConfig.MaxSize
}

// MinSize returns minimum size of the node group.
func (n *NodeGroupManager) MinSize() int {
	return n.NodeConfig.MinSize
}

// TargetSize returns the current target size of the node group. It is possible that the
// number of nodes in Kubernetes is different at the moment but should be equal
// to Size() once everything stabilizes (new nodes finish startup and registration or
// removed nodes are deleted completely). Implementation required.
func (n *NodeGroupManager) TargetSize() (int, error) {
	return n.targetSize, nil
}

// IncreaseSize increases the size of the node group. To delete a node you need
// to explicitly name it and use DeleteNode. This function should wait until
// node group size is updated. Implementation required.
func (n *NodeGroupManager) IncreaseSize(delta int) error {
	if delta <= 0 {
		return fmt.Errorf("delta must be positive, have: %d", delta)
	}

	targetSize := n.targetSize + delta

	if targetSize > n.MaxSize() {
		return fmt.Errorf("increase size request takes count beyond max size. current: %d desired: %d max: %d",
			n.currentSize, targetSize, n.MaxSize())
	}

	n.targetSize = targetSize

	ctx := context.Background()
	for i := n.currentSize + 1; i <= targetSize; i++ {
		if err := n.CreateK3sWorker(ctx, i); err != nil {
			n.FillCurrentSize(ctx)
			return err
		}
	}

	return nil
}

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
func (n *NodeGroupManager) AtomicIncreaseSize(delta int) error {
	return cloudprovider.ErrNotImplemented
}

// DeleteNodes deletes nodes from this node group. Error is returned either on
// failure or if the given node doesn't belong to this node group. This function
// should wait until node group size is updated. Implementation required.
func (n *NodeGroupManager) DeleteNodes(nodes []*apiv1.Node) error {
	ctx := context.Background()
	for _, node := range nodes {
		refId, ok1 := node.Labels[RefIdLabel]
		nodeOffsetStr, ok2 := node.Labels[OffsetLabel]
		if !(ok1 && ok2 && refId == fmt.Sprint(n.NodeConfig.RefCtrId)) {
			return fmt.Errorf("node %s does not belong to proxmox pool %s", node.Name, n.NodeConfig.TargetPool)
		}
		nodeOffset, err := strconv.Atoi(nodeOffsetStr)
		if err != nil {
			return err
		}
		if nodeOffset <= 0 {
			return fmt.Errorf("node id out of range. node name: %s", node.Name)
		}
		if err := n.DeleteCt(ctx, nodeOffset); err != nil {
			return err
		}
	}
	return nil
}

// DecreaseTargetSize decreases the target size of the node group. This function
// doesn't permit to delete any existing node and can be used only to reduce the
// request for new nodes that have not been yet fulfilled. Delta should be negative.
// It is assumed that cloud provider will not delete the existing nodes when there
// is an option to just decrease the target. Implementation required.
func (n *NodeGroupManager) DecreaseTargetSize(delta int) error {
	// TODO: ???
	return nil
}

// Id returns an unique identifier of the node group.
func (n *NodeGroupManager) Id() string {
	return n.NodeConfig.TargetPool
}

// Debug returns a string containing all information regarding this node group.
func (n *NodeGroupManager) Debug() string {
	return fmt.Sprintf("%+v\n", n)
}

// Nodes returns a list of all nodes that belong to this node group.
// It is required that Instance objects returned by this method have Id field set.
// Other fields are optional.
// This list should include also instances that might have not become a kubernetes node yet.
func (n *NodeGroupManager) Nodes() (instance []cloudprovider.Instance, err error) {
	nodes, err := n.GetPoolNodes(context.Background())
	if err != nil {
		return nil, err
	}
	for _, node := range nodes {
		instance = append(instance, cloudprovider.Instance{
			Id: node.Name,
		})
	}
	return
}

// TemplateNodeInfo returns a schedulerframework.NodeInfo structure of an empty
// (as if just started) node. This will be used in scale-up simulations to
// predict what would a new node look like if a node group was expanded. The returned
// NodeInfo is expected to have a fully populated Node object, with all of the labels,
// capacity and allocatable information as well as all pods that are started on
// the node by default, using manifest (most likely only kube-proxy). Implementation optional.
func (n *NodeGroupManager) TemplateNodeInfo() (*schedulerframework.NodeInfo, error) {
	return nil, cloudprovider.ErrNotImplemented
}

// Exist checks if the node group really exists on the cloud provider side. Allows to tell the
// theoretical node group from the real one. Implementation required.
func (n *NodeGroupManager) Exist() bool {
	return true
}

// Create creates the node group on the cloud provider side. Implementation optional.
func (n *NodeGroupManager) Create() (cloudprovider.NodeGroup, error) {
	return nil, cloudprovider.ErrNotImplemented
}

// Delete deletes the node group on the cloud provider side.
// This will be executed only for autoprovisioned node groups, once their size drops to 0.
// Implementation optional.
func (n *NodeGroupManager) Delete() error {
	return cloudprovider.ErrNotImplemented
}

// Autoprovisioned returns true if the node group is autoprovisioned. An autoprovisioned group
// was created by CA and can be deleted when scaled to 0.
func (n *NodeGroupManager) Autoprovisioned() bool {
	return false
}

// GetOptions returns NodeGroupAutoscalingOptions that should be used for this particular
// NodeGroup. Returning a nil will result in using default options.
// Implementation optional. Callers MUST handle `cloudprovider.ErrNotImplemented`.
func (n *NodeGroupManager) GetOptions(defaults config.NodeGroupAutoscalingOptions) (*config.NodeGroupAutoscalingOptions, error) {
	if n.NodeConfig.AutoScalingOptions != nil {
		return n.NodeConfig.AutoScalingOptions, nil
	}
	return nil, nil
}
