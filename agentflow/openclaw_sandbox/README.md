# OpenClaw Sandbox Plan

This folder is the safe integration boundary for a future OpenClaw runtime.

Goal:
- isolate OpenClaw from the main workstation
- allow only the minimum WeChat-related behavior
- keep AgentFlow as the control plane

## Allowed Responsibilities

- connect to WeChat through the selected connector runtime
- read inbound messages
- send outbound messages
- create group chats
- forward inbound events to AgentFlow through a narrow local API

## Not Allowed

- broad filesystem access outside its mounted working directory
- unrestricted outbound network
- arbitrary shell execution on the host
- direct access to the user home directory
- direct access to AgentFlow source except through mounted config/contracts

## Intended Deployment Shape

- run OpenClaw in a dedicated container or VM
- mount only:
  - runtime config
  - logs
  - one outbound event queue or local API credential file
- allow outbound only to:
  - WeChat endpoints required by the connector
  - AgentFlow local bridge endpoint

## AgentFlow Contract

OpenClaw should talk only to these backend endpoints:

- `POST /wechat/inbound`
- `POST /wechat/send`
- `POST /wechat/group`
- `POST /rag/signal`
- `GET /wechat/status`

## Current State

The application side of this boundary is already implemented.
The actual OpenClaw runtime installation and live API wiring still need to be done in the isolated environment.
