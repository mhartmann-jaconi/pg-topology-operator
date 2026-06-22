/*
Package cluster contains a reconciler for K8s nodes and an internal state
implementation, which keeps track of all regions and zones in the cluster.

In addition this also implements the concept of a "primary region". This is
identified by special node labels, configurable via 'topology.label_leader'.
*/
package cluster
