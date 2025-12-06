package k8s

import (
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

var Clientset *kubernetes.Clientset

func InitKubeClient() error {
	config, err := rest.InClusterConfig()
	if err != nil {
		return err
	}
	Clientset, err = kubernetes.NewForConfig(config)
	if err != nil {
		return err
	}
	return nil
}

func StringPtr(s string) *string {
	return &s
}
