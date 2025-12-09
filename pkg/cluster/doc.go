/*
Package cluster contains a reconciler for K8s nodes and an internal state
implementation, which keeps track of all zones and regions in the cluster
and if they have healthy nodes running.

In addition this also implements the concept of a "primary zone". This is
identified by special node labels, configurable via 'topology.label_leader'.
*/
package cluster
