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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/sets"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"

	"sigs.k8s.io/descheduler/pkg/api"
	"sigs.k8s.io/descheduler/pkg/descheduler/evictions"
	nodeutil "sigs.k8s.io/descheduler/pkg/descheduler/node"
	podutil "sigs.k8s.io/descheduler/pkg/descheduler/pod"
	"sigs.k8s.io/descheduler/pkg/utils"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	metricsv "k8s.io/metrics/pkg/client/clientset/versioned"
)

// LowNodeActualUtilization evicts pods from overutilized nodes to underutilized nodes. Note that CPU/Memory requests are used
// to calculate nodes' actual utilization.
func LowNodeActualUtilization(ctx context.Context, client clientset.Interface, strategy api.DeschedulerStrategy, nodes []*v1.Node, podEvictor *evictions.PodEvictor, metricsClient metricsv.Interface) {
	// TODO: May be create a struct for the strategy as well, so that we don't have to pass along the all the params?
	// garydebug
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
	if err := validateStrategyConfig1(thresholds, targetThresholds); err != nil {
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

	lowNodes, targetNodes := classifyNodes1(
		getNodeActualUsage(ctx, client, nodes, thresholds, targetThresholds, metricsClient),
		// The node has to be schedulable (to be able to move workload there)
		func(node *v1.Node, usage NodeUsage) bool {
			if nodeutil.IsNodeUnschedulable(node) {
				klog.V(2).InfoS("Node is unschedulable, thus not considered as underutilized", "node", klog.KObj(node))
				return false
			}
			return isNodeWithLowUtilization(usage)
		},
		func(node *v1.Node, usage NodeUsage) bool {
			return isNodeAboveTargetActualUtilization(usage)
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
	evictPodsFromTargetNodes1(
		ctx,
		targetNodes,
		lowNodes,
		podEvictor,
		evictable.IsEvictable,
		metricsClient,
		strategy)

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

// validateStrategyConfig checks if the strategy's config is valid
func validateStrategyConfig1(thresholds, targetThresholds api.ResourceThresholds) error {
	// validate thresholds and targetThresholds config
	if err := validateThresholds(thresholds); err != nil {
		return fmt.Errorf("thresholds config is not valid: %v", err)
	}
	if err := validateThresholds(targetThresholds); err != nil {
		return fmt.Errorf("targetThresholds config is not valid: %v", err)
	}

	// validate if thresholds and targetThresholds have same resources configured
	if len(thresholds) != len(targetThresholds) {
		return fmt.Errorf("thresholds and targetThresholds configured different resources")
	}
	for resourceName, value := range thresholds {
		if targetValue, ok := targetThresholds[resourceName]; !ok {
			return fmt.Errorf("thresholds and targetThresholds configured different resources")
		} else if value > targetValue {
			return fmt.Errorf("thresholds' %v percentage is greater than targetThresholds'", resourceName)
		}
	}
	return nil
}

// classifyNodes classifies the nodes into low-utilization or high-utilization nodes. If a node lies between
// low and high thresholds, it is simply ignored.
func classifyNodes1(
	nodeUsages []NodeUsage,
	lowThresholdFilter, highThresholdFilter func(node *v1.Node, usage NodeUsage) bool,
) ([]NodeUsage, []NodeUsage) {
	lowNodes, highNodes := []NodeUsage{}, []NodeUsage{}

	for _, nodeUsage := range nodeUsages {
		if lowThresholdFilter(nodeUsage.node, nodeUsage) {
			klog.V(2).InfoS("Node is underutilized", "node", klog.KObj(nodeUsage.node), "usage", nodeUsage.usage, "usagePercentage", resourceUsagePercentages(nodeUsage))
			lowNodes = append(lowNodes, nodeUsage)
		} else if highThresholdFilter(nodeUsage.node, nodeUsage) {
			klog.V(2).InfoS("Node is overutilized", "node", klog.KObj(nodeUsage.node), "usage", nodeUsage.usage, "usagePercentage", resourceUsagePercentages(nodeUsage))
			highNodes = append(highNodes, nodeUsage)
		} else {
			klog.V(2).InfoS("Node is appropriately utilized", "node", klog.KObj(nodeUsage.node), "usage", nodeUsage.usage, "usagePercentage", resourceUsagePercentages(nodeUsage))
		}
	}

	return lowNodes, highNodes
}

func getNodeActualUsage(
	ctx context.Context,
	client clientset.Interface,
	nodes []*v1.Node,
	lowThreshold, highThreshold api.ResourceThresholds, metricsClient metricsv.Interface,
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
			usage:   nodeActualUtilization(node, pods, metricsClient),
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
func nodeActualUtilization(node *v1.Node, pods []*v1.Pod, metricsClient metricsv.Interface) map[v1.ResourceName]*resource.Quantity {
	// metricsClient, err := client.CreateMetricsClient("/tmp/apiVersi.conf")
	nodename := node.GetName()
	nodeMetrics, err := metricsClient.MetricsV1beta1().NodeMetricses().Get(context.TODO(), nodename, metav1.GetOptions{})
	if err != nil {
		panic(err.Error())
	}
	usage := nodeMetrics.Usage

	totalReqs := map[v1.ResourceName]*resource.Quantity{
		v1.ResourceCPU:    resource.NewMilliQuantity(0, resource.DecimalSI),
		v1.ResourceMemory: resource.NewQuantity(0, resource.BinarySI),
		v1.ResourcePods:   resource.NewQuantity(int64(len(pods)), resource.DecimalSI),
	}
	for name, quantity := range usage {
		if name == v1.ResourceCPU || name == v1.ResourceMemory {
			// As Quantity.Add says: Add adds the provided y quantity to the current value. If the current value is zero,
			// the format of the quantity will be updated to the format of y.
			totalReqs[name].Add(quantity)
		}
	}
	klog.V(1).InfoS("Get node actual usage from metrics-server", "node", nodename)
	return totalReqs
}

// evictPodsFromTargetNodes evicts pods based on priority, if all the pods on the node have priority, if not
// evicts them based on QoS as fallback option.
// TODO: @ravig Break this function into smaller functions.
func evictPodsFromTargetNodes1(
	ctx context.Context,
	targetNodes, lowNodes []NodeUsage,
	podEvictor *evictions.PodEvictor,
	podFilter func(pod *v1.Pod) bool,
	metricsClient metricsv.Interface,
	strategy api.DeschedulerStrategy,
) {
	sortNodesByUsage1(targetNodes)

	// upper bound on total number of pods/cpu/memory to be moved
	totalAvailableUsage := map[v1.ResourceName]*resource.Quantity{
		v1.ResourcePods:   {},
		v1.ResourceCPU:    {},
		v1.ResourceMemory: {},
	}

	var taintsOfLowNodes = make(map[string][]v1.Taint, len(lowNodes))
	for _, node := range lowNodes {
		taintsOfLowNodes[node.node.Name] = node.node.Spec.Taints

		for name := range totalAvailableUsage {
			totalAvailableUsage[name].Add(*node.highResourceThreshold[name])
			totalAvailableUsage[name].Sub(*node.usage[name])
		}
	}

	klog.V(1).InfoS(
		"Total capacity to be moved",
		"CPU", totalAvailableUsage[v1.ResourceCPU].MilliValue(),
		"Mem", totalAvailableUsage[v1.ResourceMemory].Value(),
		"Pods", totalAvailableUsage[v1.ResourcePods].Value(),
	)

	klog.V(3).InfoS("Kind of Pod is non removable ", "Kind", sets.NewString(strategy.Params.NodeResourceActualUtilizationThresholds.ExcludeOwnerKinds...))
	for i := 0; i < len(targetNodes); i++ {
		limitTargetNumber := strategy.Params.NodeResourceActualUtilizationThresholds.LimitNumberOfTargetNodes
		if i >= limitTargetNumber {
			klog.V(3).InfoS("Error. Reached maximum number of operating targetNode per time", "limitTargetNumber", limitTargetNumber, "totalTargetNumber", len(targetNodes))
			break
		}
		node := targetNodes[i]
		klog.V(3).InfoS("Evicting pods from node", "node", klog.KObj(node.node), "usage", node.usage)

		nonRemovablePods, removablePods := classifyPods1(node.allPods, podFilter, strategy)
		klog.V(2).InfoS("Pods on node", "node", klog.KObj(node.node), "allPods", len(node.allPods), "nonRemovablePods", len(nonRemovablePods), "removablePods", len(removablePods))

		if len(removablePods) == 0 {
			klog.V(1).InfoS("No removable pods on node, try next node", "node", klog.KObj(node.node))
			continue
		}

		klog.V(1).InfoS("Evicting pods based on priority, if they have same priority, they'll be evicted based on QoS tiers")
		// sort the evictable Pods based on priority. This also sorts them based on QoS. If there are multiple pods with same priority, they are sorted based on QoS tiers.
		podutil.SortPodsBasedOnPriorityLowToHigh(removablePods)
		for i := 0; i < len(removablePods); i++ {
			pod := removablePods[i]
			klog.V(3).InfoS("removablePodsBasedOnPriorityLowToHigh", "order", i, "node", klog.KObj(node.node), "namespace", pod.GetNamespace(), "pod", pod.GetName())
		}
		evictPods1(ctx, removablePods, node, totalAvailableUsage, taintsOfLowNodes, podEvictor, metricsClient)
		klog.V(1).InfoS("Evicted pods from node", "node", klog.KObj(node.node), "evictedPods", podEvictor.NodeEvicted(node.node), "usage", node.usage)
	}
}
func evictPods1(
	ctx context.Context,
	inputPods []*v1.Pod,
	nodeUsage NodeUsage,
	totalAvailableUsage map[v1.ResourceName]*resource.Quantity,
	taintsOfLowNodes map[string][]v1.Taint,
	podEvictor *evictions.PodEvictor,
	metricsClient metricsv.Interface,
) {
	// stop if node utilization drops below target threshold or any of required capacity (cpu, memory, pods) is moved
	continueCond := func() bool {
		if !isNodeAboveTargetActualUtilization(nodeUsage) {
			return false
		}
		// if totalAvailableUsage[v1.ResourcePods].CmpInt64(0) < 1 {
		// 	return false
		// }
		if totalAvailableUsage[v1.ResourceCPU].CmpInt64(0) < 1 {
			return false
		}
		if totalAvailableUsage[v1.ResourceMemory].CmpInt64(0) < 1 {
			return false
		}
		return true
	}

	if continueCond() {
		for _, pod := range inputPods {
			if !utils.PodToleratesTaints(pod, taintsOfLowNodes) {
				klog.V(3).InfoS("Skipping eviction for pod, doesn't tolerate node taint", "pod", klog.KObj(pod))

				continue
			}
			cpuQuantity := utils.GetResourceActualQuantity(pod, v1.ResourceCPU, metricsClient)
			memoryQuantity := utils.GetResourceActualQuantity(pod, v1.ResourceMemory, metricsClient)

			klog.V(3).InfoS("Pod Actual Resource Quantity", "pod", klog.KObj(pod), "CPU", cpuQuantity.String(), "Memory", memoryQuantity.String(), "CPU(m)", cpuQuantity.MilliValue(), "Memory(Mi)", memoryQuantity.Value()/1024/1024)

			success, err := podEvictor.EvictPod(ctx, pod, nodeUsage.node, "LowNodeActualUtilization")
			if err != nil {
				klog.ErrorS(err, "Error evicting pod", "pod", klog.KObj(pod))
				break
			}

			if success {
				klog.V(3).InfoS("Evicted pods", "pod", klog.KObj(pod), "err", err)

				// cpuQuantity := utils.GetResourceRequestQuantity(pod, v1.ResourceCPU)
				nodeUsage.usage[v1.ResourceCPU].Sub(cpuQuantity)
				totalAvailableUsage[v1.ResourceCPU].Sub(cpuQuantity)

				// memoryQuantity := utils.GetResourceRequestQuantity(pod, v1.ResourceMemory)
				nodeUsage.usage[v1.ResourceMemory].Sub(memoryQuantity)
				totalAvailableUsage[v1.ResourceMemory].Sub(memoryQuantity)

				nodeUsage.usage[v1.ResourcePods].Sub(*resource.NewQuantity(1, resource.DecimalSI))
				totalAvailableUsage[v1.ResourcePods].Sub(*resource.NewQuantity(1, resource.DecimalSI))

				klog.V(3).InfoS("Updated node usage", "updatedUsage", nodeUsage)
				// check if node utilization drops below target threshold or any required capacity (cpu, memory, pods) is moved
				if !continueCond() {
					break
				}
			}
		}
	}
}

// isNodeAboveTargetActualUtilization checks if a node is overutilized
// At least one resource has to be above the high threshold
func isNodeAboveTargetActualUtilization(usage NodeUsage) bool {
	for name, nodeValue := range usage.usage {
		// usage.highResourceThreshold[name] < nodeValue
		if usage.highResourceThreshold[name].Cmp(*nodeValue) == -1 {
			return true
		}
	}
	return false
}

func classifyPods1(pods []*v1.Pod, filter func(pod *v1.Pod) bool, strategy api.DeschedulerStrategy) ([]*v1.Pod, []*v1.Pod) {
	var nonRemovablePods, removablePods []*v1.Pod

	for _, pod := range pods {
		// ownerRefs := podutil.OwnerRef(pod)
		if hasExcludedOwnerRefKind1(podutil.OwnerRef(pod), strategy) {
			nonRemovablePods = append(nonRemovablePods, pod)
		} else if !filter(pod) {
			nonRemovablePods = append(nonRemovablePods, pod)
		} else {
			removablePods = append(removablePods, pod)
		}
	}

	return nonRemovablePods, removablePods
}

// sortNodesByUsage sorts nodes based on usage in descending order
func sortNodesByUsage1(nodes []NodeUsage) {

	sort.Slice(nodes, func(i, j int) bool {
		ti := nodes[i].usage[v1.ResourceMemory].Value() + nodes[i].usage[v1.ResourceCPU].MilliValue() + nodes[i].usage[v1.ResourcePods].Value()
		tj := nodes[j].usage[v1.ResourceMemory].Value() + nodes[j].usage[v1.ResourceCPU].MilliValue() + nodes[j].usage[v1.ResourcePods].Value()
		// To return sorted in descending order
		return ti > tj
	})
}

func hasExcludedOwnerRefKind1(ownerRefs []metav1.OwnerReference, strategy api.DeschedulerStrategy) bool {
	if strategy.Params == nil || strategy.Params.NodeResourceActualUtilizationThresholds == nil {
		return false
	}
	exclude := sets.NewString(strategy.Params.NodeResourceActualUtilizationThresholds.ExcludeOwnerKinds...)
	for _, owner := range ownerRefs {
		klog.V(4).InfoS("Pod Kind", "Kind", owner.Kind, "Pod", owner.Name, "Exclude", exclude)
		if exclude.Has(owner.Kind) {
			return true
		}
	}
	return false
}

func garydebug(name string, raw interface{}) {
	data, _ := json.Marshal(raw)
	var out bytes.Buffer
	json.Indent(&out, data, "", "\t")
	fmt.Printf("%v=%v\n", name, out.String())
}
