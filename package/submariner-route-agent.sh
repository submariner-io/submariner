#!/bin/bash
set -e -x

trap "exit 1" SIGTERM SIGINT

if [ "${SUBMARINER_DEBUG}" == "true" ]; then
    DEBUG="-v=9"
else
    DEBUG="-v=4"
fi

function verify_iptables_on_host() {
    chroot /host test -x /usr/sbin/$1 && { echo "0"; return; }
    chroot /host test -x /sbin/$1 && { echo "1"; return; }
    echo "-1"
}

iptablesExists=$(verify_iptables_on_host)

# if host is mounted on /host and host has it's own iptables version
# use that one instead via the shipped iptables wrapper, we do this
# to avoid configuring iptables and nftables on the host which
# could lead to functional failures because some hosts use an iptables
# and iptables-save which program nftables under the hood. 

for f in iptables-save iptables; do
  if [ $iptablesExists == "0" ]; then
    echo "$f is present on the host at /usr/sbin/$f"
    cp /usr/sbin/${f}.wrapper /usr/sbin/$f
  elif [ $iptablesExists == "1" ]; then
    echo "$f is present on the host at /sbin/$f"
    cp /usr/sbin/${f}.sbin.wrapper /usr/sbin/$f
  else
    echo "WARNING: not using iptables wrapper because iptables was not detected on the"
    echo "host at the following paths [/usr/sbin, /sbin]."
    echo "Either the host file system isn't mounted or the host does not have iptables"
    echo "installed. The pod will use the image installed iptables version."
  fi
done

exec submariner-route-agent ${DEBUG} -alsologtostderr
