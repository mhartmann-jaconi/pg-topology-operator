package postgres

import (
	"testing"

	pgapi "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	. "github.com/onsi/gomega"
)

func TestGetStandbyInstanceCount(t *testing.T) {
	t.Run("returns zero when synchronous config is unset", func(t *testing.T) {
		g := NewWithT(t)
		cluster := &pgapi.Cluster{}

		g.Expect(getStandbyInstanceCount(cluster)).To(Equal(0))
	})

	t.Run("returns explicit number when only number is set", func(t *testing.T) {
		g := NewWithT(t)
		cluster := &pgapi.Cluster{
			Spec: pgapi.ClusterSpec{
				PostgresConfiguration: pgapi.PostgresConfiguration{
					Synchronous: &pgapi.SynchronousReplicaConfiguration{
						Number: 2,
					},
				},
			},
		}

		g.Expect(getStandbyInstanceCount(cluster)).To(Equal(2))
	})

	t.Run("returns max standby names when larger than number", func(t *testing.T) {
		g := NewWithT(t)
		maxStandby := 3
		cluster := &pgapi.Cluster{
			Spec: pgapi.ClusterSpec{
				PostgresConfiguration: pgapi.PostgresConfiguration{
					Synchronous: &pgapi.SynchronousReplicaConfiguration{
						Number:                     2,
						MaxStandbyNamesFromCluster: &maxStandby,
					},
				},
			},
		}

		g.Expect(getStandbyInstanceCount(cluster)).To(Equal(3))
	})
}
