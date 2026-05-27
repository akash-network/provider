#!/bin/bash
# Registers mock CC runtime handlers in containerd, aliased to runc.
# Run inside the Kind node via: docker exec <node> bash /path/to/this/script
set -e

if grep -q kata-qemu-snp /etc/containerd/config.toml; then
    echo "CC runtime handlers already registered"
    exit 0
fi

for rt in kata-qemu-snp kata-qemu-nvidia-gpu-snp kata-qemu-tdx kata-qemu-nvidia-gpu-tdx; do
    cat >> /etc/containerd/config.toml <<EOF

[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.${rt}]
  runtime_type = "io.containerd.runc.v2"
  [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.${rt}.options]
    SystemdCgroup = true
EOF
done

systemctl restart containerd
echo "containerd restarted with CC runtime aliases"
