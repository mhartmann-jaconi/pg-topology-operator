/*
Package pods contains a controller and a mutator implementation for K8s pods.

The controller is responsible for getting the information in which region a
pod is running and store this information into [postgres] state.

The mutator will apply a nodeSelector on the first occurrence of a primary pod
for the desired region.
*/
package pods
