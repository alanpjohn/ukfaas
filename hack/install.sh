#!/bin/bash

# Copyright OpenFaaS Author(s) 2022

set -e -x -o pipefail

export OWNER="alanpjohn"
export REPO="ukfaas"

# On CentOS /usr/local/bin is not included in the PATH when using sudo. 
# Running arkade with sudo on CentOS requires the full path
# to the arkade binary. 
export ARKADE=/usr/local/bin/arkade

# When running as a startup script (cloud-init), the HOME variable is not always set.
# As it is required for arkade to properly download tools, 
# set the variable to /usr/local so arkade will download binaries to /usr/local/.arkade
if [ -z "${HOME}" ]; then
  export HOME=/usr/local
fi

version=""

echo "Finding latest version from GitHub"
version=$(curl -sI https://github.com/$OWNER/$REPO/releases/latest | grep -i "location:" | awk -F"/" '{ printf "%s", $NF }' | tr -d '\r')
echo "$version"

if [ ! $version ]; then
  echo "Failed while attempting to get latest version"
  exit 1
fi

SUDO=sudo
if [ "$(id -u)" -eq 0 ]; then
  SUDO=
fi

verify_system() {
  if ! [ -d /run/systemd ]; then
    fatal 'Can not find systemd to use as a process supervisor for ukfaasd'
  fi
}

has_yum() {
  [ -n "$(command -v yum)" ]
}

has_apt_get() {
  [ -n "$(command -v apt-get)" ]
}

has_pacman() {
  [ -n "$(command -v pacman)" ]
}

install_required_packages() {
  if $(has_apt_get); then
    # Debian bullseye is missing iptables. Added to required packages
    # to get it working in raspberry pi. No such known issues in
    # other distros. Hence, adding only to this block.
    # reference: https://github.com/openfaas/faasd/pull/237
    $SUDO apt-get update -y
    # We will run this only on Ubuntu which comes with apt-get for the time being
    $SUDO apt-get install -y curl runc bridge-utils iptables qemu-system-x86_64
  elif $(has_yum); then
    $SUDO yum check-update -y
    $SUDO yum install -y curl runc iptables-services
  elif $(has_pacman); then
    $SUDO pacman -Syy
    $SUDO pacman -Sy curl runc bridge-utils
  else
    fatal "Could not find apt-get, yum, or pacman. Cannot install dependencies on this OS."
    exit 1
  fi
}

install_arkade(){
  curl -sLS https://get.arkade.dev | $SUDO sh
  arkade --help
}

install_cni_plugins() {
  cni_version=v0.9.1
  $SUDO $ARKADE system install cni --version ${cni_version} --path /opt/cni/bin --progress=false
}

install_containerd() {
  CONTAINERD_VER=1.7.0
  $SUDO systemctl unmask containerd || :

  arch=$(uname -m)
  if [ $arch == "armv7l" ]; then
    $SUDO curl -fSLs "https://github.com/alexellis/containerd-arm/releases/download/v${CONTAINERD_VER}/containerd-${CONTAINERD_VER}-linux-armhf.tar.gz" --output "/tmp/containerd.tar.gz"
    $SUDO tar -xvf /tmp/containerd.tar.gz -C /usr/local/bin/
    $SUDO curl -fSLs https://raw.githubusercontent.com/containerd/containerd/v${CONTAINERD_VER}/containerd.service --output "/etc/systemd/system/containerd.service"
    $SUDO systemctl enable containerd
    $SUDO systemctl start containerd
  else
    $SUDO $ARKADE system install containerd --systemd --version v${CONTAINERD_VER}  --progress=false
  fi
  
  sleep 5
}

install_ukfaasd() {
  arch=$(uname -m)
  case $arch in
  x86_64 | amd64)
    suffix=""
    ;;
  aarch64)
    suffix=-arm64
    ;;
  armv7l)
    suffix=-armhf
    ;;
  *)
    echo "Unsupported architecture $arch"
    exit 1
    ;;
  esac

  $SUDO curl -fSLs "https://github.com/$owner/$repo/releases/download/${version}/ukfaasd${suffix}" --output "/usr/local/bin/ukfaasd"
  $SUDO chmod a+x "/usr/local/bin/ukfaasd"

  mkdir -p /tmp/ukfaasd-${version}-installation/hack
  cd /tmp/ukfaasd-${version}-installation
  $SUDO curl -fSLs "https://raw.githubusercontent.com/$owner/$repo/${version}/docker-compose.yaml" --output "docker-compose.yaml"
  $SUDO curl -fSLs "https://raw.githubusercontent.com/$owner/$repo/${version}/prometheus.yml" --output "prometheus.yml"
  $SUDO curl -fSLs "https://raw.githubusercontent.com/$owner/$repo/${version}/resolv.conf" --output "resolv.conf"
  $SUDO curl -fSLs "https://raw.githubusercontent.com/$owner/$repo/${version}/hack/ukfaasd-provider.service" --output "hack/ukfaasd-provider.service"
  $SUDO curl -fSLs "https://raw.githubusercontent.com/$owner/$repo/${version}/hack/ukfaasd.service" --output "hack/ukfaasd.service"
  $SUDO /usr/local/bin/ukfaasd install
}

install_caddy() {
  if [ ! -z "${UKFAASD_DOMAIN}" ]; then
    CADDY_VER=v2.4.3
    arkade get --progress=false caddy -v ${CADDY_VER}
    
    # /usr/bin/caddy is specified in the upstream service file.
    $SUDO install -m 755 $HOME/.arkade/bin/caddy /usr/bin/caddy

    $SUDO curl -fSLs https://raw.githubusercontent.com/caddyserver/dist/master/init/caddy.service --output /etc/systemd/system/caddy.service

    $SUDO mkdir -p /etc/caddy
    $SUDO mkdir -p /var/lib/caddy
    
    if $(id caddy >/dev/null 2>&1); then
      echo "User caddy already exists."
    else
      $SUDO useradd --system --home /var/lib/caddy --shell /bin/false caddy
    fi

    $SUDO tee /etc/caddy/Caddyfile >/dev/null <<EOF
{
  email "${LETSENCRYPT_EMAIL}"
}

${UKFAASD_DOMAIN} {
  reverse_proxy 127.0.0.1:8080
}
EOF

    $SUDO chown --recursive caddy:caddy /var/lib/caddy
    $SUDO chown --recursive caddy:caddy /etc/caddy

    $SUDO systemctl enable caddy
    $SUDO systemctl start caddy
  else
    echo "Skipping caddy installation as UKFAASD_DOMAIN."
  fi
}

install_faas_cli() {
  arkade get --progress=false faas-cli
  $SUDO install -m 755 $HOME/.arkade/bin/faas-cli /usr/local/bin/
}

verify_system
install_required_packages

$SUDO /sbin/sysctl -w net.ipv4.conf.all.forwarding=1
echo "net.ipv4.conf.all.forwarding=1" | $SUDO tee -a /etc/sysctl.conf

install_arkade
install_cni_plugins
install_containerd
install_faas_cli
install_ukfaasd
install_caddy
