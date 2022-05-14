# Image layer and signature mirroring

This directory contains configuration and code for mirroring layer and
signature objects of already-promoted container images to one or more
non-Google object stores.

The `oci-proxy` running registry.k8s.io is able to detect when a client that is
pulling an OCI image is calling from a non-Google source IP address (e.g. an
EC2 instance running in an AWS region). When `oci-proxy` detects these calling
clients, it returns a `302 Redirect` to the caller for image layers residing in
a object store mirror in the local region. This dramatically reduces the
Kubernetes infrastructure costs due to eliminating a large portion of egress
bandwidth costs from Google datacenters (running GCR and GCS) to the non-Google
calling clients.

