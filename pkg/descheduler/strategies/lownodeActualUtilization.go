/*
Copyright 2017 The Kubernetes Authors.

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

package strategies

import (
	"context"
	"fmt"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"

	"sigs.k8s.io/descheduler/pkg/api"
	"sigs.k8s.io/descheduler/pkg/descheduler/client"
	"sigs.k8s.io/descheduler/pkg/descheduler/evictions"
	nodeutil "sigs.k8s.io/descheduler/pkg/descheduler/node"
	podutil "sigs.k8s.io/descheduler/pkg/descheduler/pod"
	"sigs.k8s.io/descheduler/pkg/utils"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// LowNodeUtilization evicts pods from overutilized nodes to underutilized nodes. Note that CPU/Memory requests are used
// to calculate nodes' utilization and not the actual resource usage.
func LowNodeActualUtilization(ctx context.Context, client clientset.Interface, strategy api.DeschedulerStrategy, nodes []*v1.Node, podEvictor *evictions.PodEvictor) {
	// TODO: May be create a struct for the strategy as well, so that we don't have to pass along the all the params?
	// gary
	fmt.Println("lownodeactualutilization.go func LowNodeActualUtilization=====================================")
	if err := validateLowNodeActualUtilizationParams(strategy.Params); err != nil {
		klog.ErrorS(err, "Invalid LowNodeUtilization parameters")
		return
	}
	// default priority class: system-cluster-critical
	thresholdPriority, err := utils.GetPriorityFromStrategyParams(ctx, client, strategy.Params)
	if err != nil {
		klog.ErrorS(err, "Failed to get threshold priority from strategy's params")
		return
	}

	thresholds := strategy.Params.NodeResourceActualUtilizationThresholds.Thresholds
	targetThresholds := strategy.Params.NodeResourceActualUtilizationThresholds.TargetThresholds
	if err := validateStrategyConfig(thresholds, targetThresholds); err != nil {
		klog.ErrorS(err, "LowNodeUtilization config is not valid")
		return
	}
	// check if Pods/CPU/Mem are set, if not, set them to 100
	if _, ok := thresholds[v1.ResourcePods]; !ok {
		thresholds[v1.ResourcePods] = MaxResourcePercentage
		targetThresholds[v1.ResourcePods] = MaxResourcePercentage
	}
	if _, ok := thresholds[v1.ResourceCPU]; !ok {
		thresholds[v1.ResourceCPU] = MaxResourcePercentage
		targetThresholds[v1.ResourceCPU] = MaxResourcePercentage
	}
	if _, ok := thresholds[v1.ResourceMemory]; !ok {
		thresholds[v1.ResourceMemory] = MaxResourcePercentage
		targetThresholds[v1.ResourceMemory] = MaxResourcePercentage
	}

	lowNodes, targetNodes := classifyNodes(
		getNodeUsage(ctx, client, nodes, thresholds, targetThresholds),
		// The node has to be schedulable (to be able to move workload there)
		func(node *v1.Node, usage NodeUsage) bool {
			if nodeutil.IsNodeUnschedulable(node) {
				klog.V(2).InfoS("Node is unschedulable, thus not considered as underutilized", "node", klog.KObj(node))
				return false
			}
			return isNodeWithLowUtilization(usage)
		},
		func(node *v1.Node, usage NodeUsage) bool {
			return isNodeAboveTargetUtilization(usage)
		},
	)

	klog.V(1).InfoS("Criteria for a node under utilization",
		"CPU", thresholds[v1.ResourceCPU], "Mem", thresholds[v1.ResourceMemory], "Pods", thresholds[v1.ResourcePods])

	if len(lowNodes) == 0 {
		klog.V(1).InfoS("No node is underutilized, nothing to do here, you might tune your thresholds further")
		return
	}
	klog.V(1).InfoS("Total number of underutilized nodes", "totalNumber", len(lowNodes))

	if len(lowNodes) < strategy.Params.NodeResourceActualUtilizationThresholds.NumberOfNodes {
		klog.V(1).InfoS("Number of nodes underutilized is less than NumberOfNodes, nothing to do here", "underutilizedNodes", len(lowNodes), "numberOfNodes", strategy.Params.NodeResourceActualUtilizationThresholds.NumberOfNodes)
		return
	}

	if len(lowNodes) == len(nodes) {
		klog.V(1).InfoS("All nodes are underutilized, nothing to do here")
		return
	}

	if len(targetNodes) == 0 {
		klog.V(1).InfoS("All nodes are under target utilization, nothing to do here")
		return
	}

	klog.V(1).InfoS("Criteria for a node above target utilization",
		"CPU", targetThresholds[v1.ResourceCPU], "Mem", targetThresholds[v1.ResourceMemory], "Pods", targetThresholds[v1.ResourcePods])

	klog.V(1).InfoS("Number of nodes above target utilization", "totalNumber", len(targetNodes))
	evictable := podEvictor.Evictable(evictions.WithPriorityThreshold(thresholdPriority))

	evictPodsFromTargetNodes(
		ctx,
		targetNodes,
		lowNodes,
		podEvictor,
		evictable.IsEvictable)

	klog.V(1).InfoS("Total number of pods evicted", "evictedPods", podEvictor.TotalEvicted())
}

func validateLowNodeActualUtilizationParams(params *api.StrategyParameters) error {
	if params == nil || params.NodeResourceActualUtilizationThresholds == nil {
		return fmt.Errorf("NodeResourceActualUtilizationThresholds not set")
	}
	if params.ThresholdPriority != nil && params.ThresholdPriorityClassName != "" {
		return fmt.Errorf("only one of thresholdPriority and thresholdPriorityClassName can be set")
	}

	return nil
}

func getNodeActualUsage(
	ctx context.Context,
	client clientset.Interface,
	nodes []*v1.Node,
	lowThreshold, highThreshold api.ResourceThresholds,
) []NodeUsage {
	nodeUsageList := []NodeUsage{}

	for _, node := range nodes {
		pods, err := podutil.ListPodsOnANode(ctx, client, node)
		if err != nil {
			klog.V(2).InfoS("Node will not be processed, error accessing its pods", "node", klog.KObj(node), "err", err)
			continue
		}

		nodeCapacity := node.Status.Capacity
		if len(node.Status.Allocatable) > 0 {
			nodeCapacity = node.Status.Allocatable
		}

		nodeUsageList = append(nodeUsageList, NodeUsage{
			node:    node,
			usage:   nodeActualUtilization(node),
			allPods: pods,
			// A threshold is in percentages but in <0;100> interval.
			// Performing `threshold * 0.01` will convert <0;100> interval into <0;1>.
			// Multiplying it with capacity will give fraction of the capacity corresponding to the given high/low resource threshold in Quantity units.
			lowResourceThreshold: map[v1.ResourceName]*resource.Quantity{
				v1.ResourceCPU:    resource.NewMilliQuantity(int64(float64(lowThreshold[v1.ResourceCPU])*float64(nodeCapacity.Cpu().MilliValue())*0.01), resource.DecimalSI),
				v1.ResourceMemory: resource.NewQuantity(int64(float64(lowThreshold[v1.ResourceMemory])*float64(nodeCapacity.Memory().Value())*0.01), resource.BinarySI),
				v1.ResourcePods:   resource.NewQuantity(int64(float64(lowThreshold[v1.ResourcePods])*float64(nodeCapacity.Pods().Value())*0.01), resource.DecimalSI),
			},
			highResourceThreshold: map[v1.ResourceName]*resource.Quantity{
				v1.ResourceCPU:    resource.NewMilliQuantity(int64(float64(highThreshold[v1.ResourceCPU])*float64(nodeCapacity.Cpu().MilliValue())*0.01), resource.DecimalSI),
				v1.ResourceMemory: resource.NewQuantity(int64(float64(highThreshold[v1.ResourceMemory])*float64(nodeCapacity.Memory().Value())*0.01), resource.BinarySI),
				v1.ResourcePods:   resource.NewQuantity(int64(float64(highThreshold[v1.ResourcePods])*float64(nodeCapacity.Pods().Value())*0.01), resource.DecimalSI),
			},
		})
	}

	return nodeUsageList
}

// The main differrence with lownodeutilization.go
func nodeActualUtilization(node *v1.Node) map[v1.ResourceName]*resource.Quantity {
	metricsClient, err := client.CreateMetricsClient("/tmp/apiVersi.conf")
	if err != nil {
		panic(err.Error())
	}
	nodename := node.GetName()
	nodeMetrics, err := metricsClient.MetricsV1beta1().NodeMetricses().Get(context.TODO(), nodename, metav1.GetOptions{})
	usage := nodeMetrics.Usage

	totalReqs := map[v1.ResourceName]*resource.Quantity{
		v1.ResourceCPU:    resource.NewMilliQuantity(0, resource.DecimalSI),
		v1.ResourceMemory: resource.NewQuantity(0, resource.BinarySI),
	}
	for name, quantity := range usage {
		if name == v1.ResourceCPU || name == v1.ResourceMemory {
			// As Quantity.Add says: Add adds the provided y quantity to the current value. If the current value is zero,
			// the format of the quantity will be updated to the format of y.
			totalReqs[name].Add(quantity)
		}
	}
	return totalReqs
}
