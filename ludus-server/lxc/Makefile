BASEDIR = $(shell dab basedir)
LUDUS_VERSION ?= 0.0.0-dev

.PHONY: all bootstrap install finalize clean

all: bootstrap install finalize

bootstrap:
	dab init
	dab bootstrap --minimal

install:
	dab install python3 python3-pip python3-venv ansible-core wireguard-tools \
	  dnsmasq nftables iproute2 openssh-client sshpass curl jq ca-certificates \
	  git gpg dbus systemd-resolved sudo
	# system user
	dab exec groupadd -g 1001 ludus
	dab exec useradd -u 1001 -g 1001 -m -s /bin/bash ludus
	# dirs
	dab exec mkdir -p /opt/ludus/install /opt/ludus/db /opt/ludus/tls \
	  /opt/ludus/users /opt/ludus/collections /opt/ludus/resources/packer/plugins
	# binary + embedded trees
	install -m 0755 ../../binaries/ludus-server $(BASEDIR)/opt/ludus/ludus-server
	cp -r ../ansible $(BASEDIR)/opt/ludus/ansible
	cp -r ../packer $(BASEDIR)/opt/ludus/packer
	# python venv (air-gap)
	dab exec python3 -m venv /opt/ludus/venv
	dab exec /opt/ludus/venv/bin/pip install --no-cache-dir \
	  proxmoxer==2.0.1 requests==2.32.3 netaddr==1.2.1 pywinrm==0.4.3 \
	  dnspython==2.6.1 jmespath==1.0.1
	# galaxy collections (air-gap)
	dab exec env ANSIBLE_COLLECTIONS_PATH=/opt/ludus/collections \
	  ansible-galaxy collection install -r /opt/ludus/ansible/requirements.yml \
	  -p /opt/ludus/collections
	# packer + plugins
	install -m 0755 deps/packer $(BASEDIR)/usr/local/bin/packer
	cp deps/packer-plugin-* $(BASEDIR)/opt/ludus/resources/packer/plugins/
	# systemd + sysctl
	install -m 0644 files/ludus.service $(BASEDIR)/etc/systemd/system/ludus.service
	install -m 0644 files/ludus-admin.service $(BASEDIR)/etc/systemd/system/ludus-admin.service
	install -m 0644 files/99-ludus-sysctl.conf $(BASEDIR)/etc/sysctl.d/99-ludus.conf
	dab exec systemctl enable ludus.service ludus-admin.service wg-quick@wg0 dnsmasq
	dab exec chown -R ludus:ludus /opt/ludus

finalize:
	dab finalize --compressor zstd-max

clean:
	dab clean
	rm -f *.tar.zst
