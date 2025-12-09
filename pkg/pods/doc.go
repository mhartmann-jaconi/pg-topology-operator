/*
Package pods contains a controller and a mutator implementation for K8s pods.

The controller is responsible for getting the information in which zone a
pod is running and store this information into [postgres.SetZoneForInstanceId].

The mutator will apply a nodeSelector on the pod for the desired zone.
*/
package pods
