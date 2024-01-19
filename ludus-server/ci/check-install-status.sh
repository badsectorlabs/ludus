#!/bin/bash

sudo su -

if [[ -f /opt/ludus/install/.stage-3-complete ]]; then 
    echo 'Ludus install completed successfully'
else 
    tail -n 10 /opt/ludus/install/install.log
fi
