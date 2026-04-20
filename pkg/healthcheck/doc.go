// This package contains a healtcheck implementation for K8s controllers,
// based on K8s' leases.
// It writes a RenewTime every 'writeInterval' seconds to the lease and
// waits for the lease to get reconciled by the Reconciler implementation.
// If reconcilation was successful, the Watchdog object get's the reconile
// time as 'lastSeen'.
// The healtcheck then evaluates the 'Healthy' function of the watchdog,
// which checks if the lastSeen time was more than 'maxAge' ago.
package healthcheck
