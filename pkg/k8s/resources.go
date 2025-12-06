package k8s

import (
	"context"
	"fmt"
	"log"
	"net"
	"time"

	"github.com/2025-softbank-hackathon-final-navy/execution/config"
	"github.com/2025-softbank-hackathon-final-navy/execution/pkg/types"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/util/retry"
)

func CreateK8sResources(name string, req types.FunctionRequest) error {
	ctx := context.TODO()
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: name}, Data: map[string]string{"user_code.py": req.Code}}
	_, err := Clientset.CoreV1().ConfigMaps(config.NAMESPACE).Create(ctx, cm, metav1.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		log.Printf("Failed to create ConfigMap: %v", err)
	}

	memReq := resource.MustParse("128Mi")
	targetLabel := "small"

	if req.Request == "large-memory" {
		memReq = resource.MustParse("1Gi")
		targetLabel = "large"
	}

	replicas := int32(1)
	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": name}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": name}},
				Spec: corev1.PodSpec{
					RuntimeClassName: StringPtr("gvisor"),
					NodeSelector:     map[string]string{"mem_type": targetLabel},
					Containers: []corev1.Container{{
						Name:            "runner",
						Image:           config.RUNNER_IMAGE,
						ImagePullPolicy: corev1.PullNever,
						Ports:           []corev1.ContainerPort{{ContainerPort: 8080}},
						Resources:       corev1.ResourceRequirements{Requests: corev1.ResourceList{"memory": memReq}},
						VolumeMounts:    []corev1.VolumeMount{{Name: "code-vol", MountPath: "/app/user_code.py", SubPath: "user_code.py"}},
						ReadinessProbe: &corev1.Probe{
							ProbeHandler:        corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: "/", Port: intstr.FromInt(8080)}},
							InitialDelaySeconds: 1,
							PeriodSeconds:       1,
						},
					}},
					Volumes: []corev1.Volume{{
						Name: "code-vol",
						VolumeSource: corev1.VolumeSource{
							ConfigMap: &corev1.ConfigMapVolumeSource{
								LocalObjectReference: corev1.LocalObjectReference{Name: name},
							},
						},
					}},
				},
			},
		},
	}
	_, err = Clientset.AppsV1().Deployments(config.NAMESPACE).Create(ctx, deploy, metav1.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create deployment: %w", err)
	}

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       corev1.ServiceSpec{Selector: map[string]string{"app": name}, Ports: []corev1.ServicePort{{Port: 8080, TargetPort: intstr.FromInt(8080)}}},
	}
	_, err = Clientset.CoreV1().Services(config.NAMESPACE).Create(ctx, svc, metav1.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create service: %w", err)
	}
	return nil
}

func DeleteK8sResources(name string) error {
	ctx := context.TODO()
	delOpt := metav1.DeleteOptions{}
	Clientset.AppsV1().Deployments(config.NAMESPACE).Delete(ctx, name, delOpt)
	Clientset.CoreV1().Services(config.NAMESPACE).Delete(ctx, name, delOpt)
	Clientset.CoreV1().ConfigMaps(config.NAMESPACE).Delete(ctx, name, delOpt)
	return nil
}

func WaitForPodReady(name string) bool {
	targetAddr := fmt.Sprintf("%s.%s.svc.cluster.local:8080", name, config.NAMESPACE)
	timeout := 30 * time.Second
	start := time.Now()
	for {
		if time.Since(start) > timeout {
			return false
		}
		conn, err := net.DialTimeout("tcp", targetAddr, 200*time.Millisecond)
		if err == nil {
			conn.Close()
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func ScaleUpDeployment(funcID string, needed int32) {
	if needed > config.MAX_REPLICAS {
		needed = config.MAX_REPLICAS
	}
	retry.RetryOnConflict(retry.DefaultRetry, func() error {
		deploy, err := Clientset.AppsV1().Deployments(config.NAMESPACE).Get(context.TODO(), funcID, metav1.GetOptions{})
		if err != nil {
			return err
		}
		if *deploy.Spec.Replicas < needed {
			deploy.Spec.Replicas = &needed
			_, err = Clientset.AppsV1().Deployments(config.NAMESPACE).Update(context.TODO(), deploy, metav1.UpdateOptions{})
			return err
		}
		return nil
	})
}
