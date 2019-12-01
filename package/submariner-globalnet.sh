#!/bin/bash
set -e -x

trap "exit 1" SIGTERM SIGINT

if [ "${SUBMARINER_DEBUG}" == "true" ]; then
    DEBUG="-v=9"
else
    DEBUG="-v=4"
fi

function find_iptables_on_host() {
    chroot /host test -x /usr/sbin/$1 && { echo "/usr/sbin"; return; }
    chroot /host test -x /sbin/$1 && { echo "/sbin"; return; }
    echo "unknown"
}


# if host is mounted on /host and host has it's own iptables version
# use that one instead via the shipped iptables wrapper, we do this
# to avoid configuring iptables and nftables on the host which
# could lead to functional failures because some hosts use an iptables
# and iptables-save which program nftables under the hood.

for f in iptables-save iptables; do
  iptablesLocation=$(find_iptables_on_host $f)
  if [ $iptablesLocation == "/usr/sbin" ]; then
    echo "$f is present on the host at /usr/sbin/$f"
    cp /usr/sbin/${f}.wrapper /usr/sbin/$f
  elif [ $iptablesLocation == "/sbin" ]; then
    echo "$f is present on the host at /sbin/$f"
    cp /usr/sbin/${f}.sbin.wrapper /usr/sbin/$f
  else
    echo "WARNING: not using iptables wrapper because iptables was not detected on the"
    echo "host at the following paths [/usr/sbin, /sbin]."
    echo "Either the host file system isn't mounted or the host does not have iptables"
    echo "installed. The pod will use the image installed iptables version."
  fi
done

exec submariner-globalnet ${DEBUG} -alsologtostderr