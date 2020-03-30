/*
Copyright 2017 Pusher Ltd.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package nodes

import (
	"sort"
	"strings"

	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	kube_client "k8s.io/client-go/kubernetes"
)

var (
	// OnDemandNodeLabel label for on-demand instances.
	OnDemandNodeLabel = "kubernetes.io/role=worker"
	// SpotNodeLabel label for spot instances.
	SpotNodeLabel = "kubernetes.io/role=spot-worker"
	// OnDemand key for on-demand instances of NodesMap.
	OnDemand NodeType
	// Spot key for spot instances of NodesMap.
	Spot NodeType = 1
	// Lowest Priority considered on spot nodes
	PriorityThreshold = 0
)

// NodeInfo struct containing node and it's pods as well information
// resources on the node.
type NodeInfo struct {
	Node         *apiv1.Node
	Pods         []*apiv1.Pod
	RequestedCPU int64
	FreeCPU      int64
}

// NodeType integer key for keying NodesMap.
type NodeType int

// NodeInfoArray array of NodeInfo pointers.
type NodeInfoArray []*NodeInfo

// Map map of NodeInfoArray.
type Map map[NodeType]NodeInfoArray

// NewNodeMap creates a new NodesMap from a list of Nodes.
func NewNodeMap(client kube_client.Interface, nodes []*apiv1.Node) (Map, error) {
	nodeMap := Map{
		OnDemand: make([]*NodeInfo, 0),
		Spot:     make([]*NodeInfo, 0),
	}

	for _, node := range nodes {
		nodeInfo, err := newNodeInfo(client, node)
		if err != nil {
			return nil, err
		}

		// Sort pods with biggest CPU request first
		sort.Slice(nodeInfo.Pods, func(i, j int) bool {
			iCPU := getPodCPURequests(nodeInfo.Pods[i])
			jCPU := getPodCPURequests(nodeInfo.Pods[j])
			return iCPU > jCPU
		})

		switch true {
		case isSpotNode(node):
			nodeMap[Spot] = append(nodeMap[Spot], nodeInfo)
			continue
		case isOnDemandNode(node):
			nodeMap[OnDemand] = append(nodeMap[OnDemand], nodeInfo)
			continue
		default:
			continue
		}
	}

	// Sort spot nodes by most requested CPU first
	sort.Slice(nodeMap[Spot], func(i, j int) bool {
		return nodeMap[Spot][i].RequestedCPU > nodeMap[Spot][j].RequestedCPU
	})
	// Sort on-demand nodes by least requested CPU first
	sort.Slice(nodeMap[OnDemand], func(i, j int) bool {
		return nodeMap[OnDemand][i].RequestedCPU < nodeMap[OnDemand][j].RequestedCPU
	})

	return nodeMap, nil
}

func newNodeInfo(client kube_client.Interface, node *apiv1.Node) (*NodeInfo, error) {
	pods, err := getPodsOnNode(client, node)
	if err != nil {
		return nil, err
	}
	requestedCPU := calculateRequestedCPU(pods)

	return &NodeInfo{
		Node:         node,
		Pods:         pods,
		RequestedCPU: requestedCPU,
		FreeCPU:      node.Status.Allocatable.Cpu().MilliValue() - requestedCPU,
	}, nil
}

// AddPod adds a pod to a NodeInfo and updates the relevant resource values.
func (n *NodeInfo) AddPod(pod *apiv1.Pod) {
	n.Pods = append(n.Pods, pod)
	n.RequestedCPU = calculateRequestedCPU(n.Pods)
	n.FreeCPU = n.Node.Status.Allocatable.Cpu().MilliValue() - n.RequestedCPU
}

// Gets a list of pods that are running on the given node
func getPodsOnNode(client kube_client.Interface, node *apiv1.Node) ([]*apiv1.Pod, error) {
	podsOnNode, err := client.CoreV1().Pods(apiv1.NamespaceAll).List(
		metav1.ListOptions{FieldSelector: fields.SelectorFromSet(fields.Set{"spec.nodeName": node.Name}).String()})
	if err != nil {
		return []*apiv1.Pod{}, err
	}

	pods := make([]*apiv1.Pod, 0)
	for i := range podsOnNode.Items {
		// Ignore pods with priority below threshold on spot nodes
		if int(*podsOnNode.Items[i].Spec.Priority) < PriorityThreshold && isSpotNode(node) {
			continue
		}
		pods = append(pods, &podsOnNode.Items[i])
	}
	return pods, nil
}

// Works out requested CPU for a collection of pods and returns it in MilliValue
// (Pod requests are stored as MilliValues hence the return type here)
func calculateRequestedCPU(pods []*apiv1.Pod) int64 {
	var CPURequests int64
	for _, pod := range pods {
		CPURequests += getPodCPURequests(pod)
	}
	return CPURequests
}

// Returns the total requested CPU  for all of the containers in a given Pod.
// (Returned as MilliValues)
func getPodCPURequests(pod *apiv1.Pod) int64 {
	var CPUTotal int64
	for _, container := range pod.Spec.Containers {
		CPUTotal += container.Resources.Requests.Cpu().MilliValue()
	}
	return CPUTotal
}

// Determines if a node has the spotNodeLabel assigned
func isSpotNode(node *apiv1.Node) bool {
	splitLabel := strings.SplitN(SpotNodeLabel, "=", 2)

	// If "=" found, check for new label schema. If no "=" is found, check for
	// old label schema
	switch len(splitLabel) {
	case 1:
		_, found := node.ObjectMeta.Labels[SpotNodeLabel]
		return found
	case 2:
		spotLabelKey := splitLabel[0]
		spotLabelVal := splitLabel[1]

		val, _ := node.ObjectMeta.Labels[spotLabelKey]
		if val == spotLabelVal {
			return true
		}
	}
	return false
}

// Determines if a node has the OnDemandNodeLabel assigned
func isOnDemandNode(node *apiv1.Node) bool {
	splitLabel := strings.SplitN(OnDemandNodeLabel, "=", 2)

	// If "=" found, check for new label schema. If no "=" is found, check for
	// old label schema
	switch len(splitLabel) {
	case 1:
		_, found := node.ObjectMeta.Labels[OnDemandNodeLabel]
		return found
	case 2:
		onDemandLabelKey := splitLabel[0]
		onDemandLabelVal := splitLabel[1]

		val, _ := node.ObjectMeta.Labels[onDemandLabelKey]
		if val == onDemandLabelVal {
			return true
		}
	}
	return false
}

// CopyNodeInfos returns an array of copies of the NodeInfos in this array.
func (n NodeInfoArray) CopyNodeInfos() NodeInfoArray {
	var arr NodeInfoArray
	for _, node := range n {
		nodeInfo := &NodeInfo{
			Node:         node.Node,
			Pods:         node.Pods,
			RequestedCPU: node.RequestedCPU,
			FreeCPU:      node.FreeCPU,
		}
		arr = append(arr, nodeInfo)
	}
	return arr
}
