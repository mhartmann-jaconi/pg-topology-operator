package pvcs

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"

	"github.com/jaconi-io/ipoque-pg-webhook/pkg/cluster"
	"github.com/jaconi-io/ipoque-pg-webhook/pkg/config"
	"github.com/jaconi-io/ipoque-pg-webhook/pkg/postgres"
	"github.com/jaconi-io/ipoque-pg-webhook/pkg/webhook"
	"github.com/spf13/viper"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
)

const mutationEndpoint string = "/mutate-pvcs"
const mutationConfigName string = "postgres-pvc-update"

func BuildWebhook(caCert *bytes.Buffer) *admissionregistrationv1.MutatingWebhookConfiguration {
	apiGroup := corev1.SchemeGroupVersion.Group
	apiVersion := corev1.SchemeGroupVersion.Version
	apiResource := string(corev1.ResourcePersistentVolumeClaims)
	return webhook.Build(caCert, mutationEndpoint, mutationConfigName, apiGroup, apiVersion, apiResource)
}

func Setup(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &corev1.PersistentVolumeClaim{}).
		WithDefaulter(&defaulter{}).
		WithDefaulterCustomPath(mutationEndpoint).
		Complete()
}

type defaulter struct {
}

func (defaulter) Default(_ context.Context, pvc *corev1.PersistentVolumeClaim) error {
	if _, ok := pvc.Labels[postgres.LabelPGClusterName]; !ok {
		return nil
	}

	if err := changeVolume(pvc); err != nil {
		slog.Error(fmt.Sprintf("Failed to update PVC: %v", err))
		return err
	}
	return nil
}

// changeVolume will create a copy of the given persistent volume and change the storage class
// of that volume to the configured one for the pvc's region
func changeVolume(pvc *corev1.PersistentVolumeClaim) error {
	instanceId := postgres.GetInstanceId(pvc.Labels)
	clusterName := postgres.GetClusterName(pvc.Labels)
	primaryId := postgres.GetCurrentPrimaryInstanceId(clusterName)

	instanceZone := ""
	var err error

	if primaryId == "" {
		// no primary instance was initialized yet, assuming this PVC is the
		// first one and therefore used for the primary instance
		instanceZone = cluster.GetPrimaryZone()
		_, err = postgres.SetZoneForInstanceIdIgnoreHealth(clusterName, instanceId, cluster.GetPrimaryZone())
	} else {
		// either assign the currently stored zone for this instance ID or get the
		// least used zone and apply it
		instanceZone, err = postgres.GetOrAssignZoneToInstance(clusterName, instanceId)
	}
	if err != nil {
		return err
	}

	pvc.ObjectMeta.Labels[viper.GetString(config.LabelZone)] = instanceZone
	return applyStorageClass(pvc, instanceZone)
}

func applyStorageClass(pvc *corev1.PersistentVolumeClaim, zone string) error {
	storageClass := cluster.GetStorageClassForZone(zone)
	if storageClass != "" {
		slog.Info(fmt.Sprintf("Set storage class %s to volume PVC %s", storageClass, pvc.Name))
		pvc.Spec.StorageClassName = &storageClass
	} else {
		cluster.LogZones()
		return fmt.Errorf("no storage class for zone %s", zone)
	}
	return nil
}
