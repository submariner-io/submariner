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


# If host is mounted on /host and host has its own iptables version
# use that one instead via the shipped iptables wrapper, we do this
# to avoid configuring iptables and nftables on the host which
# could lead to functional failures because some hosts use an iptables
# and iptables-save which program nftables under the hood.
# Since we're using UBI8, we only have nftables in the container so we
# can't use the Kubernetes approach (see
# https://github.com/kubernetes/kubernetes/pull/82966 and
# https://github.com/kubernetes/website/pull/16271 for details).

for f in iptables-save iptables; do
  location=$(find_iptables_on_host $f)
  if [ "${location}" != "unknown" ]; then
    echo "$f is present on the host at ${location}/$f"
	sed "s!@@PATH@@!${location}!" /usr/sbin/iptables-wrapper.in > /usr/sbin/$f
  else
    echo "WARNING: not using iptables wrapper because iptables was not detected on the"
    echo "host at the following paths [/usr/sbin, /sbin]."
    echo "Either the host file system isn't mounted or the host does not have iptables"
    echo "installed. The pod will use the image installed iptables version."
  fi
done

exec submariner-globalnet ${DEBUG} -alsologtostderr
