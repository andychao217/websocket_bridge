# 使用官方的Golang镜像作为基础镜像  
# Copyright (c) Abstract Machines
# SPDX-License-Identifier: Apache-2.0

FROM --platform=$BUILDPLATFORM golang:1.22.2-alpine AS builder

# 设置GOPROXY环境变量
ENV GOPROXY=https://goproxy.cn,https://goproxy.io,https://mirrors.aliyun.com/goproxy/,https://proxy.golang.org,direct

ARG TARGETOS
ARG TARGETARCH
ARG SVC

WORKDIR /app

COPY . .

RUN echo http://mirrors.aliyun.com/alpine/v3.19/main/ > /etc/apk/repositories && \
    echo http://mirrors.aliyun.com/alpine/v3.19/community/ >> /etc/apk/repositories

#Build
RUN apk update \
    && apk upgrade \
    && apk add make\
    && make GOOS=$TARGETOS GOARCH=$TARGETARCH $SVC \
    && mv build/$SVC /exe

FROM scratch
COPY --from=builder /exe /

ENTRYPOINT ["/exe"]









