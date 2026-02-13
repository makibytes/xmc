FROM golang:1.23-bookworm

ARG MQ_VRMF=9.4.5.0
ARG MQ_ARCH=X64
ARG MQ_RDURL=https://public.dhe.ibm.com/ibmdl/export/pub/software/websphere/messaging/mqdev/redist

ENV PATH=/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
ENV genmqpkg_incnls=1
ENV genmqpkg_incsdk=1
ENV genmqpkg_inctls=1

RUN apt-get update \
  && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    tar \
    gzip \
  && rm -rf /var/lib/apt/lists/*

RUN mkdir -p /opt/mqm \
  && cd /opt/mqm \
  && curl -fLO "${MQ_RDURL}/${MQ_VRMF}-IBM-MQC-Redist-Linux${MQ_ARCH}.tar.gz" \
  && tar -zxf ./*.tar.gz \
  && rm -f ./*.tar.gz \
  && /opt/mqm/bin/genmqpkg.sh -b /opt/mqm

ENV CGO_CFLAGS=-I/opt/mqm/inc
ENV CGO_LDFLAGS=-L/opt/mqm/lib64

WORKDIR /workspace
