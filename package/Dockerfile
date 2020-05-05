FROM fedora:31

WORKDIR /var/submariner

RUN dnf -y distrosync --nodocs --setopt=install_weak_deps=False && \
    dnf -y install --nodocs --setopt=install_weak_deps=False \
                   iproute iptables strongswan procps-ng && \
    dnf -y clean all && \
    rpm -e gnupg2 rpm-sign-libs gpgme dnf libdnf yum python3-rpm \
           python3-dnf python3-gpg librepo python3-libdnf python3-hawkey \
           glib2 libmodulemd libsolv libyaml libassuan \
           shadow-utils tss2 ima-evm-utils \
           zchunk-libs vim-minimal npth sudo tar libusbx acl dnf-data \
           libksba libreport-filesystem libsemanage libstdc++ openssl \
           python3-libcomps rpm-build-libs sssd-client

COPY submariner.sh submariner-engine /usr/local/bin/

ENTRYPOINT submariner.sh
