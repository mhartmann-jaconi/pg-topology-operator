package util

import (
	v1 "k8s.io/api/core/v1"
)

func PodIsHealthy(pod *v1.Pod) bool {
	if pod.Status.Phase != v1.PodRunning {
		return false
	}
	for _, con := range pod.Status.ContainerStatuses {
		if !con.Ready {
			return false
		}
	}
	return true
}
