BASEDIR = $(shell dab basedir)
LUDUS_VERSION ?= 0.0.0-dev

.PHONY: all bootstrap install finalize clean

all: bootstrap install finalize

bootstrap:
	dab init
	dab bootstrap --minimal

install:
	dab install python3 python3-pip python3-venv wireguard-tools \
	  dnsmasq nftables iproute2 openssh-client sshpass curl jq ca-certificates \
	  git gpg dbus systemd-resolved sudo xorriso
	# system user
	dab exec groupadd -g 1001 ludus
	dab exec useradd -u 1001 -g 1001 -m -s /bin/bash ludus
	# dirs
	dab exec mkdir -p /opt/ludus/install /opt/ludus/db /opt/ludus/tls \
	  /opt/ludus/users /opt/ludus/resources/global-collections \
	  /opt/ludus/resources/global-roles /opt/ludus/resources/packer/plugins
	# binary + embedded trees
	install -m 0755 ../../binaries/ludus-server $(BASEDIR)/opt/ludus/ludus-server
	cp -r ../ansible $(BASEDIR)/opt/ludus/ansible
	cp -r ../packer $(BASEDIR)/opt/ludus/packer
	# python venv (air-gap)
	install -m 0644 python-requirements.txt $(BASEDIR)/opt/ludus/install/python-requirements.txt
	cp -r deps/python-wheels $(BASEDIR)/opt/ludus/install/python-wheels
	dab exec python3 -m venv /opt/ludus/venv
	dab exec /opt/ludus/venv/bin/pip install --no-index --find-links=/opt/ludus/install/python-wheels \
	  -r /opt/ludus/install/python-requirements.txt
	dab exec rm -rf /opt/ludus/install/python-wheels /opt/ludus/install/python-requirements.txt
	# galaxy collections (air-gap)
	cp -r deps/collections/ansible_collections $(BASEDIR)/opt/ludus/resources/global-collections/
	cp -r deps/roles/* $(BASEDIR)/opt/ludus/resources/global-roles/
	chmod 0755 $(BASEDIR)/opt/ludus/ansible/range-management/dynamic-inventory
	# packer + plugins
	install -m 0755 deps/packer $(BASEDIR)/usr/local/bin/packer
	cp -r deps/packer-plugins/. $(BASEDIR)/opt/ludus/resources/packer/plugins/
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
