package client

import (
	"fmt"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	metricsv "k8s.io/metrics/pkg/client/clientset/versioned"
)

func CreateMetricsClient(kubeconfig string) (*metricsv.Clientset, error) {
	var cfg *rest.Config
	if len(kubeconfig) != 0 {
		master, err := GetMasterFromKubeconfig(kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("Failed to parse kubeconfig file: %v ", err)
		}

		cfg, err = clientcmd.BuildConfigFromFlags(master, kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("Unable to build config: %v", err)
		}

	} else {
		var err error
		cfg, err = rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("Unable to build in cluster config: %v", err)
		}
	}
	return metricsv.NewForConfig(cfg)
}
