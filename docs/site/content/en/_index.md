---
title: agent-peer
weight: 1
layout: hextra-home
---

{{< hextra/hero-headline >}}
Cross-host messaging for Claude Code sessions
{{< /hextra/hero-headline >}}

{{< hextra/hero-subtitle >}}
agent-peer lets Claude Code sessions running on different hosts discover each other and exchange text messages.
{{< /hextra/hero-subtitle >}}

{{< hextra/hero-button text="Quick start" link="docs/getting-started" >}}
{{< hextra/hero-button text="GitHub" link="https://github.com/wangzw/agent-peer" >}}

{{< hextra/feature-grid cols="3" >}}
  {{< hextra/feature-card title="Cross-host messaging" subtitle="Sessions on different hosts send each other plain-text messages through their local Brokers." >}}
  {{< hextra/feature-card title="Topics decide visibility" subtitle="Two sessions see each other only when they share at least one topic." >}}
  {{< hextra/feature-card title="Pairing with fingerprints" subtitle="Two users pair their Brokers with a pairing key. Each side then recognizes the other by its public-key fingerprint." >}}
  {{< hextra/feature-card title="Direct connections" subtitle="Brokers connect directly. Messages are not relayed through a third Broker, and there is no central registry." >}}
  {{< hextra/feature-card title="System or user Broker" subtitle="One Broker can serve every allowed account on a host as a system service, or a single account as an ordinary process." >}}
  {{< hextra/feature-card title="Idle sessions start working" subtitle="A message written to an idle session's inbox socket starts it working without user input; this was observed on Claude Code v2.1.327, and there is no compatibility promise for other versions." >}}
{{< /hextra/feature-grid >}}
