#!/bin/bash

if sudo test -f /opt/ludus/install/.stage-3-complete && ! sudo test -f /etc/systemd/system/ludus-install.service; then
    echo 'Ludus install completed successfully'
else
    sudo tail -n 10 /opt/ludus/install/install.log
fi
