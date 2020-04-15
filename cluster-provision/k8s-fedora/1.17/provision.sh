#!/bin/bash

set -ex

cni_manifest="/tmp/cni.yaml"

# Kubernetes rqeuierment: Set SELinux in permissive mode (effectively disabling it)
setenforce 0
sed -i "s/^SELINUX=.*/SELINUX=permissive/" /etc/selinux/config

# Kubernetes rqeuierment: Disable swap
swapoff -a
sed -i '/ swap / s/^/#/' /etc/fstab

systemctl stop firewalld || :
systemctl disable firewalld || :
# Make sure the firewall is never enabled again
# Enabling the firewall destroys the iptable rules
yum -y remove firewalld

# Required for iscsi demo to work.
yum -y install iscsi-initiator-utils

# Install docker required packages.
yum -y install yum-utils \
    device-mapper-persistent-data \
    lvm2

# Add Docker repository.
yum-config-manager --add-repo \
  https://download.docker.com/linux/centos/docker-ce.repo

# Install Docker CE.
yum -y install \
  containerd.io-1.2.10 \
  docker-ce-19.03.4 \
  docker-ce-cli-19.03.4

# Create /etc/docker directory.
mkdir /etc/docker

# Setup docker daemon
cat << EOF > /etc/docker/daemon.json
{
  "insecure-registries" : ["registry:5000"],
  "log-driver": "json-file",
  "exec-opts": ["native.cgroupdriver=systemd"]
}
EOF

mkdir -p /etc/systemd/system/docker.service.d

# Restart Docker
systemctl daemon-reload
systemctl restart docker

# Add Kubernetes repository.
cat << EOF > /etc/yum.repos.d/kubernetes.repo
[kubernetes]
name=Kubernetes
baseurl=https://packages.cloud.google.com/yum/repos/kubernetes-el7-x86_64
enabled=1
gpgcheck=1
repo_gpgcheck=1
gpgkey=https://packages.cloud.google.com/yum/doc/yum-key.gpg https://packages.cloud.google.com/yum/doc/rpm-package-key.gpg
EOF

setenforce 0
sed -i 's/^SELINUX=enforcing$/SELINUX=permissive/' /etc/selinux/config

# Install Kubernetes packages.
yum install --nogpgcheck --disableexcludes=kubernetes -y \
    kubeadm-${version} \
    kubelet-${version} \
    kubectl-${version} \
    kubernetes-cni-0.6.0

# Ensure iptables tooling does not use the nftables backend
update-alternatives --set iptables /usr/sbin/iptables-legacy

# Enable and state kubelet service
systemctl enable --now kubelet

# TODO use config file! this is deprecated
cat <<EOT >/etc/sysconfig/kubelet
KUBELET_EXTRA_ARGS=--cgroup-driver=systemd --runtime-cgroups=/systemd/system.slice --kubelet-cgroups=/systemd/system.slice --feature-gates="BlockVolume=true,CSIBlockVolume=true,VolumeSnapshotDataSource=true"
EOT

# Start and enable docker and kubelet services, before instllation
systemctl daemon-reload

systemctl enable docker && systemctl start docker
systemctl enable kubelet && systemctl start kubelet

# Needed for kubernetes service routing and dns
# https://github.com/kubernetes/kubernetes/issues/33798#issuecomment-250962627
modprobe bridge
modprobe br_netfilter
cat <<EOF >  /etc/sysctl.d/k8s.conf
net.bridge.bridge-nf-call-ip6tables = 1
net.bridge.bridge-nf-call-iptables = 1
net.ipv4.ip_forward = 1
EOF
sysctl --system

echo bridge >> /etc/modules
echo br_netfilter >> /etc/modules

# configure additional settings for cni plugin
cat <<EOF >/etc/NetworkManager/conf.d/001-calico.conf
[keyfile]
unmanaged-devices=interface-name:cali*;interface-name:tunl*
EOF

# Use dhclient to have expected hostname behaviour
cat <<EOF >/etc/NetworkManager/conf.d/002-dhclient.conf
[main]
dhcp=dhclient
EOF

sysctl -w net.netfilter.nf_conntrack_max=1000000
echo "net.netfilter.nf_conntrack_max=1000000" >> /etc/sysctl.conf

systemctl restart NetworkManager
mkdir -p /tmp/kubeadm-patches/

cat >/tmp/kubeadm-patches/kustomization.yaml <<EOF
patchesJson6902:
- target:
    version: v1
    kind: Pod
    name: kube-apiserver
    namespace: kube-system
  path: add-security-context.yaml
- target:
    version: v1
    kind: Pod
    name: kube-controller-manager
    namespace: kube-system
  path: add-security-context.yaml
- target:
    version: v1
    kind: Pod
    name: kube-scheduler
    namespace: kube-system
  path: add-security-context.yaml
- target:
    version: v1
    kind: Pod
    name: etcd
    namespace: kube-system
  path: add-security-context.yaml
EOF

cat >/tmp/kubeadm-patches/add-security-context.yaml <<EOF
- op: add
  path: /spec/securityContext
  value:
    seLinuxOptions:
      type: spc_t
EOF

cat >/tmp/kubeadm-patches/add-security-context-deployment-patch.yaml <<EOF
spec:
  template:
    spec:
      securityContext:
        seLinuxOptions:
          type: spc_t
EOF


default_cidr="192.168.0.0/16"
pod_cidr="10.244.0.0/16"
kubeadm init --pod-network-cidr=$pod_cidr --kubernetes-version v${version} --token abcdef.1234567890123456 --experimental-kustomize /tmp/kubeadm-patches/

kubectl --kubeconfig=/etc/kubernetes/admin.conf patch deployment coredns -n kube-system -p "$(cat /tmp/kubeadm-patches/add-security-context-deployment-patch.yaml)"
sed -i -e "s?$default_cidr?$pod_cidr?g" $cni_manifest
kubectl --kubeconfig=/etc/kubernetes/admin.conf create -f "$cni_manifest"

# Wait at least for 7 pods
while [[ "$(kubectl --kubeconfig=/etc/kubernetes/admin.conf get pods -n kube-system --no-headers | wc -l)" -lt 7 ]]; do
    echo "Waiting for at least 7 pods to appear ..."
    kubectl --kubeconfig=/etc/kubernetes/admin.conf get pods -n kube-system
    sleep 10
done

# Wait until k8s pods are running
while [ -n "$(kubectl --kubeconfig=/etc/kubernetes/admin.conf get pods -n kube-system --no-headers | grep -v Running)" ]; do
    echo "Waiting for k8s pods to enter the Running state ..."
    kubectl --kubeconfig=/etc/kubernetes/admin.conf get pods -n kube-system --no-headers | >&2 grep -v Running || true
    sleep 10
done

# Make sure all containers are ready
while [ -n "$(kubectl --kubeconfig=/etc/kubernetes/admin.conf get pods -n kube-system -o'custom-columns=status:status.containerStatuses[*].ready,metadata:metadata.name' --no-headers | grep false)" ]; do
    echo "Waiting for all containers to become ready ..."
    kubectl --kubeconfig=/etc/kubernetes/admin.conf get pods -n kube-system -o'custom-columns=status:status.containerStatuses[*].ready,metadata:metadata.name' --no-headers
    sleep 10
done

kubectl --kubeconfig=/etc/kubernetes/admin.conf get pods -n kube-system

reset_command="kubeadm reset"
admission_flag="admission-control"
# k8s 1.11 asks for confirmation on kubeadm reset, which can be suppressed by a new force flag
reset_command="kubeadm reset --force"

# k8s 1.11 uses new flags for admission plugins
# old one is deprecated only, but can not be combined with new one, which is used in api server config created by kubeadm
admission_flag="enable-admission-plugins"

$reset_command

# audit log configuration
mkdir /etc/kubernetes/audit

audit_api_version="audit.k8s.io/v1"
cat > /etc/kubernetes/audit/adv-audit.yaml <<EOF
apiVersion: ${audit_api_version}
kind: Policy
rules:
- level: Request
  users: ["kubernetes-admin"]
  resources:
  - group: kubevirt.io
    resources:
    - virtualmachines
    - virtualmachineinstances
    - virtualmachineinstancereplicasets
    - virtualmachineinstancepresets
    - virtualmachineinstancemigrations
  omitStages:
  - RequestReceived
  - ResponseStarted
  - Panic
EOF

cat > /etc/kubernetes/kubeadm.conf <<EOF
apiVersion: kubeadm.k8s.io/v1beta1
bootstrapTokens:
- groups:
  - system:bootstrappers:kubeadm:default-node-token
  token: abcdef.1234567890123456
  ttl: 24h0m0s
  usages:
  - signing
  - authentication
kind: InitConfiguration
---
apiServer:
  extraArgs:
    allow-privileged: "true"
    audit-log-format: json
    audit-log-path: /var/log/k8s-audit/k8s-audit.log
    audit-policy-file: /etc/kubernetes/audit/adv-audit.yaml
    enable-admission-plugins: NamespaceLifecycle,LimitRanger,ServiceAccount,DefaultStorageClass,DefaultTolerationSeconds,NodeRestriction,MutatingAdmissionWebhook,ValidatingAdmissionWebhook,ResourceQuota
    feature-gates: BlockVolume=true,CSIBlockVolume=true,VolumeSnapshotDataSource=true,AdvancedAuditing=true
  extraVolumes:
  - hostPath: /etc/kubernetes/audit
    mountPath: /etc/kubernetes/audit
    name: audit-conf
    readOnly: true
  - hostPath: /var/log/k8s-audit
    mountPath: /var/log/k8s-audit
    name: audit-log
  timeoutForControlPlane: 4m0s
apiVersion: kubeadm.k8s.io/v1beta1
certificatesDir: /etc/kubernetes/pki
clusterName: kubernetes
controlPlaneEndpoint: ""
controllerManager:
  extraArgs:
    feature-gates: BlockVolume=true,CSIBlockVolume=true,VolumeSnapshotDataSource=true
dns:
  type: CoreDNS
etcd:
  local:
    dataDir: /var/lib/etcd
imageRepository: k8s.gcr.io
kind: ClusterConfiguration
kubernetesVersion: ${version}
networking:
  dnsDomain: cluster.local
  podSubnet: 10.244.0.0/16
  serviceSubnet: 10.96.0.0/12
EOF

# Create local-volume directories
for i in {1..10}
do
  mkdir -p /var/local/kubevirt-storage/local-volume/disk${i}
  mkdir -p /mnt/local-storage/local/disk${i}
  echo "/var/local/kubevirt-storage/local-volume/disk${i} /mnt/local-storage/local/disk${i} none defaults,bind 0 0" >> /etc/fstab
done
chmod -R 777 /var/local/kubevirt-storage/local-volume

# Setup selinux permissions to local volume directories.
chcon -R unconfined_u:object_r:svirt_sandbox_file_t:s0 /mnt/local-storage/

# Pre pull fluentd image used in logging
docker pull fluent/fluentd:v1.2-debian
docker pull fluent/fluentd-kubernetes-daemonset:v1.2-debian-syslog

# Pre pull images used in Ceph CSI
docker pull quay.io/k8scsi/csi-attacher:v1.0.1
docker pull quay.io/k8scsi/csi-provisioner:v1.0.1
docker pull quay.io/k8scsi/csi-snapshotter:v1.0.1
docker pull quay.io/cephcsi/rbdplugin:v1.0.0
docker pull quay.io/k8scsi/csi-node-driver-registrar:v1.0.2

# Pre pull cluster network addons operator images and store manifests
# so we can use them at cluster-up
cp -rf /tmp/cnao/ /opt/
for i in $(grep -A 2 "IMAGE" /opt/cnao/operator.yaml |grep value | awk '{print $2}'); do docker pull $i; done



























































































































































































































