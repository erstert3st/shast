# Sandbox image for shast. Built by `shast image build`; the tag is derived
# from a hash of this directory, so any change here yields a new tag.
#
# Tool versions must not drift, because the expected outputs belong to the
# image tag: the base image is pinned by digest, and apt installs from the
# snapshot.debian.org date that this base image names in debian.sources
# (a new digest brings a new snapshot date).
FROM debian:trixie-slim@sha256:a99cfc517144bc59b1978475ec53b46ecabec7e43635402ee5b77cc54cd1b20a

RUN sed -i -E '/^# http:\/\/snapshot/{s/^# /URIs: /;n;d}' /etc/apt/sources.list.d/debian.sources \
 && apt-get -o Acquire::Check-Valid-Until=false update \
 && apt-get install -y --no-install-recommends \
      bc bsdextrautils coreutils diffutils file findutils gawk git grep gzip \
      jq less procps sed tar tree unzip util-linux xz-utils zip \
 && rm -rf /var/lib/apt/lists/* \
 && find / -xdev -type f -perm /6000 -exec chmod a-s {} +

RUN groupadd --gid 1000 dev \
 && useradd --uid 1000 --gid 1000 --home-dir /tmp --no-create-home --shell /bin/bash dev \
 && install -d -o dev -g dev -m 0755 /seed /work \
 && install -o dev -g dev -m 0644 /dev/null /seed.sha256

COPY gitconfig /etc/gitconfig
COPY seed.sh /usr/local/lib/shast/seed.sh

ENV TZ=UTC LANG=C.UTF-8 LC_ALL=C.UTF-8 HOME=/tmp PAGER=cat GIT_PAGER=cat

USER dev
RUN bash /usr/local/lib/shast/seed.sh /seed /seed.sha256

WORKDIR /work
CMD ["sleep", "14400"]
