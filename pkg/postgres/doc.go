/*
Package postgres contains
  - controller for storing PG cluster information
  - internal state of PG cluster information

The controller gets latest PG cluster information, such as
  - name of the cluster
  - current primary instance
  - number of sync-replicas

and stores this information internally. If a cluster is deleted in K8s
this will also remove it from the internal state.

If a cluster becomes healthy again, this will also start a re-evaluation
of the target primary instance of the cluster. The reason for that is,
that cloudnative-pg changes the primary instance in case of a switch-over
and does not change it back to the original instance after doing that.
Unfortunately we do require that because the primary instance should run
in the primary AZ.
*/
package postgres
