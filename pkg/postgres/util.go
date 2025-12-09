package postgres

import (
	"strings"

	pgapi "github.com/cloudnative-pg/cloudnative-pg/api/v1"
)

const (
	LabelPGInstanceRole string = "cnpg.io/instanceRole"
	LabelPGInstanceName string = "cnpg.io/instanceName"
	LabelPGClusterName  string = "cnpg.io/cluster"
	LabelPGJobRole      string = "cnpg.io/jobRole"
	LabelPGRole         string = "role"

	PGRolePrimary string = "primary"
	PGRoleReplica string = "replica"
)

func IsPrimary(labels map[string]string) bool {
	return labels[LabelPGInstanceRole] == "primary" ||
		labels[LabelPGJobRole] == "initdb"
}

func GetInstanceId(labels map[string]string) string {
	clusterName := labels[LabelPGClusterName]
	instanceName := labels[LabelPGInstanceName]
	return ExtractInstanceId(clusterName, instanceName)
}

func ExtractInstanceId(clusterName, instanceName string) string {
	if instanceName == "" {
		return ""
	}
	sTemp := strings.ReplaceAll(instanceName, clusterName, "")
	return strings.ReplaceAll(sTemp, "-", "")
}

func GetClusterName(labels map[string]string) string {
	return labels[LabelPGClusterName]
}

func isClusterHealthy(clus *pgapi.Cluster) bool {
	return strings.EqualFold(clus.Status.Phase, "Cluster in healthy state")
}
