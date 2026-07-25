---
title: First-class central daemon clients
status: active
---

## Goal

Make one-poller/many-reader usagent deployments safe by adding an explicit client destination and a fail-closed require-daemon policy shared by CLI and MCP, while preserving existing daemon-first and local-only behavior.
