package postgres

import (
	"fmt"
	"log/slog"
	"strings"

	pgapi "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	LabelPGInstanceRole string = "cnpg.io/instanceRole"
	LabelPGInstanceName string = "cnpg.io/instanceName"
	LabelPGClusterName  string = "cnpg.io/cluster"
	LabelPGJobRole      string = "cnpg.io/jobRole"
	LabelPGRole         string = "role"

	PGRolePrimary string = "primary"
	PGRoleReplica string = "replica"
	PGRoleInitDB  string = "initdb"
)

// IsPrimary tells if a k8s resource with a given label belongs to the current PG
// primary instance.
func IsPrimary(labels map[string]string) bool {
	return labels[LabelPGInstanceRole] == PGRolePrimary ||
		labels[LabelPGJobRole] == PGRoleInitDB
}

// GetInstanceId extracts a string value of the numerical instance ID for a K8s resource
// with the given labels
func GetInstanceId(labels map[string]string) string {
	clusterName := labels[LabelPGClusterName]
	instanceName := labels[LabelPGInstanceName]
	return ExtractInstanceId(clusterName, instanceName)
}

// ExtractInstanceId retrieves the string value of a numerical ID of a PG instance.
func ExtractInstanceId(clusterName, instanceName string) string {
	if instanceName == "" {
		return ""
	}
	sTemp := strings.ReplaceAll(instanceName, clusterName, "")
	return strings.ReplaceAll(sTemp, "-", "")
}

// GetClusterName extracts the name of the PG cluster, a K8s resource with the given
// labels is attached to
func GetClusterName(labels map[string]string) string {
	return labels[LabelPGClusterName]
}

func ReadDesiredPrimaryInstanceID(clusterName, clusterNamespace string) (string, error) {
	clus, err := readCluster(clusterNamespace, clusterName)
	if err != nil {
		return "", err
	}
	return ExtractInstanceId(clusterName, clus.Status.TargetPrimary), nil
}

// IsHealthy retrieves if a PG cluster with given name and namespace is considered 'healthy'
func IsHealthy(clusterName, clusterNamespace string) bool {
	clus, err := readCluster(clusterNamespace, clusterName)
	if err != nil {
		if !errors.IsNotFound(err) {
			slog.Error(fmt.Sprintf("Failed to read cluster %s: %v", clusterName, err))
		}
		return false
	}
	return isClusterHealthy(clus)
}

func isClusterHealthy(clus *pgapi.Cluster) bool {
	// all status conditions must be 'true'
	for _, con := range clus.Status.Conditions {
		if con.Status != metav1.ConditionTrue {
			return false
		}
	}
	// current primary instance must match desired primary instance
	if clus.Status.CurrentPrimary != clus.Status.TargetPrimary {
		return false
	}
	return strings.EqualFold(clus.Status.Phase, "Cluster in healthy state")
}
