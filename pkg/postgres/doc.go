/*
Package postgres contains
  - controller for storing PG cluster information
  - internal state of PG cluster information
  - primary PG instance selection
  - webhook for intercept primary instance selection

The controller gets latest PG cluster information, such as
  - name of the cluster
  - current primary instance
  - number of sync-replicas

and stores this information internally. If a cluster is deleted in K8s
this will also remove it from the internal state.

The webhook watches status changes on a PG cluster and checks if a new
desired primary instance is in the proper region. If not, this desired
instance is automatically switched to an eligible one.
*/
package postgres
