package postgres

import (
	"strings"
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
