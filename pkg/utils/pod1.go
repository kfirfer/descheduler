package utils

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	metricsv "k8s.io/metrics/pkg/client/clientset/versioned"
)

// GetResourceActualQuantity finds and returns the actual quantity for a specific resource.
func GetResourceActualQuantity(pod *v1.Pod, resourceName v1.ResourceName, metricsClient metricsv.Interface) resource.Quantity {
	actualQuantity := resource.Quantity{}

	switch resourceName {
	case v1.ResourceCPU:
		actualQuantity = resource.Quantity{Format: resource.DecimalSI}
	case v1.ResourceMemory, v1.ResourceStorage, v1.ResourceEphemeralStorage:
		actualQuantity = resource.Quantity{Format: resource.BinarySI}
	default:
		actualQuantity = resource.Quantity{Format: resource.DecimalSI}
	}

	podname := pod.GetName()
	podnamesapce := pod.GetNamespace()
	nodeMetrics, err := metricsClient.MetricsV1beta1().PodMetricses(podnamesapce).Get(context.Background(), podname, metav1.GetOptions{})
	if err != nil {
		panic(err.Error())
	}
	for _, containerMetrics := range nodeMetrics.Containers {
		usage := containerMetrics.Usage
		
		switch resourceName {
		case v1.ResourceCPU:
			actualQuantity.Add(*usage.Cpu())
		case v1.ResourceMemory, v1.ResourceStorage, v1.ResourceEphemeralStorage:
			actualQuantity.Add(*usage.Memory())
		default:
			fmt.Println("Error GetResourceActualQuantity")
		}
	}

	// totalReqs := map[v1.ResourceName]*resource.Quantity{
	// 	v1.ResourceCPU:    resource.NewMilliQuantity(0, resource.DecimalSI),
	// 	v1.ResourceMemory: resource.NewQuantity(0, resource.BinarySI),
	// }
	// for name, quantity := range usage {
	// 	if name == v1.ResourceCPU || name == v1.ResourceMemory {
	// 		// As Quantity.Add says: Add adds the provided y quantity to the current value. If the current value is zero,
	// 		// the format of the quantity will be updated to the format of y.
	// 		totalReqs[name].Add(quantity)
	// 	}
	// }

	// if resourceName == v1.ResourceEphemeralStorage && !utilfeature.DefaultFeatureGate.Enabled(LocalStorageCapacityIsolation) {
	// 	// if the local storage capacity isolation feature gate is disabled, pods request 0 disk
	// 	return requestQuantity
	// }

	// for _, container := range pod.Spec.Containers {
	// 	if rQuantity, ok := container.Resources.Requests[resourceName]; ok {
	// 		requestQuantity.Add(rQuantity)
	// 	}
	// }

	// for _, container := range pod.Spec.InitContainers {
	// 	if rQuantity, ok := container.Resources.Requests[resourceName]; ok {
	// 		if requestQuantity.Cmp(rQuantity) < 0 {
	// 			requestQuantity = rQuantity.DeepCopy()
	// 		}
	// 	}
	// }

	// // if PodOverhead feature is supported, add overhead for running a pod
	// // to the total requests if the resource total is non-zero
	// if pod.Spec.Overhead != nil && utilfeature.DefaultFeatureGate.Enabled(PodOverhead) {
	// 	if podOverhead, ok := pod.Spec.Overhead[resourceName]; ok && !requestQuantity.IsZero() {
	// 		requestQuantity.Add(podOverhead)
	// 	}
	// }

	return actualQuantity
}
