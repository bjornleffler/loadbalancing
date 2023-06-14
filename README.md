# Load balancing utilities

Utilities for load balancing of scale out storage solutions, such as the [knsfd NFS proxy](https://github.com/GoogleCloudPlatform/knfsd-cache-utils).

## vip_manager
VIP Manager is a utility to manage virtual ("[alias](https://cloud.google.com/vpc/docs/alias-ip)") IPs for instances in a [Managed Instance Group](https://cloud.google.com/compute/docs/instance-groups). Virtual IPs are distributed in an even way between all the nodes in the instance group. When the number of nodes changes, Virtual IPs are automatically re-balanced.

### Build
```go build vip_manager.go```

### Run
```
vip_manager \
  -project PROJECT \
  -zone GCE_ZONE \
  -gce_instance_group INSTANCE_GROUP_NAME \
  -alias_network NAME_OF_ALIAS_NETWORK \
  -vips NETWORK_PREFIX
```

Replace works in ALL_CAPS with values for your environment.

Project and GCE zone are auto configured inside [Google Cloud Platform](https://cloud.google.com) ([GCE](https://cloud.google.com/compute) or [GKE](https://cloud.google.com/kubernetes-engine)).

### Permissions
vip_manager needs permissions to:
1. List GCE instances and instance groups.
2. Add and remove alias IPs to/from GCE instances.

These permissions are not included in "Compute Engine Read Write" nor "Allow full access to all Cloud APIs" when creating a VM. One way to allow vip_manager to run inside a VM in GCE/GKE is to grant the "Compute Instance Admin (v1)" role to the GCE service account (PROJECT_NUMBER@project.gserviceaccount.com).

(TODO: Figure out a better way)

## metrics_exporter
Metrics Exporter is a utility to export system metrics for load balancing, for example to load balance connections based on current NFS connections.

### Build
```go build metrics_exporter.go```

### Run
```
metrics_exporter [-p PORT]
```

The default port is 9001.

### Manual test
```
curl http://IP:PORT/metrics
```
Replace IP and PORT.
