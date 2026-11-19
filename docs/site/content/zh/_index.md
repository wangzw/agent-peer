---
title: agent-peer
weight: 1
layout: hextra-home
---

{{< hextra/hero-headline >}}
在不同主机上的 Claude Code 会话之间互发消息
{{< /hextra/hero-headline >}}

{{< hextra/hero-subtitle >}}
agent-peer 让运行在不同主机上的 Claude Code 会话互相发现，并交换文本消息。
{{< /hextra/hero-subtitle >}}

{{< hextra/hero-button text="快速开始" link="docs/getting-started" >}}
{{< hextra/hero-button text="GitHub" link="https://github.com/wangzw/agent-peer" >}}

{{< hextra/feature-grid cols="3" >}}
  {{< hextra/feature-card title="跨主机互发消息" subtitle="不同主机上的会话经本机 Broker 互发纯文本消息。" >}}
  {{< hextra/feature-card title="topic 决定可见范围" subtitle="两个会话至少同在一个 topic 时才能互相看见。" >}}
  {{< hextra/feature-card title="凭指纹配对" subtitle="两位用户用配对密钥让两个 Broker 配对，之后各自凭对方的公钥指纹确认身份。" >}}
  {{< hextra/feature-card title="直接连接" subtitle="Broker 之间直接连接，消息不经第三个 Broker 转发，也没有集中式注册中心。" >}}
  {{< hextra/feature-card title="系统级与用户级 Broker" subtitle="一个 Broker 可以作为系统服务，服务主机上所有获准的账户；也可以作为普通进程，只服务一个账户。" >}}
  {{< hextra/feature-card title="空闲会话自动开始处理" subtitle="写入空闲会话 inbox socket 的消息会让它无需用户输入就开始处理；这一行为在 Claude Code v2.1.327 上观测到，不承诺兼容其他版本。" >}}
{{< /hextra/feature-grid >}}
