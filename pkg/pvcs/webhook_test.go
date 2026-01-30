package pvcs

import (
	"context"
	"testing"

	"github.com/jaconi-io/ipoque-pg-webhook/pkg/cluster"
	"github.com/jaconi-io/ipoque-pg-webhook/pkg/config"
	"github.com/jaconi-io/ipoque-pg-webhook/pkg/events"
	"github.com/jaconi-io/ipoque-pg-webhook/pkg/postgres"
	. "github.com/onsi/gomega"
	"github.com/spf13/viper"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestChangeVolume(t *testing.T) {
	g := NewWithT(t)

	events.SetListenersForAZChange(func(ctx context.Context, s string) {})

	viper.Set(config.PgVolumes, map[string]string{"east": "nfs-east", "west": "nfs-west"})
	defer viper.Reset()

	postgres.CreateOrUpdateCluster("pg", "default", "", 1)
	cluster.Store("node1", "west-1", "west", false)
	cluster.Store("node2", "east-1", "east", true)

	// first PVC should be treated as primary
	pvc1 := createPvc("1")
	err := changeVolume(pvc1)
	g.Expect(err).To(BeNil())
	g.Expect(*pvc1.Spec.StorageClassName).To(Equal("nfs-east"))

	// set primary manually
	postgres.CreateOrUpdateCluster("pg", "default", "1", 1)

	// second one should be created in the other zone, as this is unused yet
	pvc2 := createPvc("2")
	err = changeVolume(pvc2)
	g.Expect(err).To(BeNil())
	g.Expect(*pvc2.Spec.StorageClassName).To(Equal("nfs-west"))

	cluster.Store("node3", "west-2", "west", false)
	pvc3 := createPvc("3")
	err = changeVolume(pvc3)
	g.Expect(err).To(BeNil())
	g.Expect(*pvc3.Spec.StorageClassName).To(Equal("nfs-west"))
}

func createPvc(idx string) *corev1.PersistentVolumeClaim {
	pd := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pg-" + idx,
			Namespace: "default",
			Labels: map[string]string{
				postgres.LabelPGClusterName:  "pg",
				postgres.LabelPGInstanceName: "pg-" + idx,
			},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			StorageClassName: nil,
		},
	}
	return pd
}
