/*
Package pvcs contains a mutator which will set a storage class to the incoming
PVC, based on the desired region it should run in.
In addition this also reconciles PVCs to fill the internal state map with zone
and instance information.
*/
package pvcs
