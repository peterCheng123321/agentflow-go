import asyncio
import os
import shutil
import subprocess
from enum import Enum


def resolve_openclaw_bin():
    env_bin = os.getenv("OPENCLAW_BIN")
    if env_bin and os.path.isfile(env_bin):
        return env_bin

    home_bin = os.path.expanduser("~/.openclaw/bin/openclaw")
    if os.path.isfile(home_bin):
        return home_bin

    return shutil.which("openclaw")


OPENCLAW_BIN = resolve_openclaw_bin()
OPENCLAW_AVAILABLE = bool(OPENCLAW_BIN)


class WeChatStatus(Enum):
    DISCONNECTED = "disconnected"
    CONNECTING = "connecting"
    AUTHENTICATING = "authenticating"
    CONNECTED = "connected"
    PAUSED = "paused"


class BaseWeChatAdapter:
    mode_name = "base"

    async def login(self, connector):
        raise NotImplementedError

    async def send_message(self, connector, contact_name, text):
        raise NotImplementedError

    async def list_contacts(self, connector):
        return []

    async def create_group_chat(self, connector, group_name, members):
        return False, "Not implemented", {}

    async def simulate_incoming_message(self, connector, contact_name, text):
        if connector.message_handler:
            await connector.message_handler({"from": contact_name, "text": text})


class MockWeChatAdapter(BaseWeChatAdapter):
    mode_name = "mock"

    async def login(self, connector):
        await asyncio.sleep(0.1)
        connector.status = WeChatStatus.CONNECTED
        return True

    async def send_message(self, connector, contact_name, text):
        if connector.status != WeChatStatus.CONNECTED:
            return False, "Not connected"
        return True, "Sent (mock)"

    async def list_contacts(self, connector):
        return [
            {"name": "Alice Zhang", "wechat_id": "alice-001"},
            {"name": "ClientX", "wechat_id": "clientx-001"},
            {"name": "File Transfer", "wechat_id": "file-transfer"},
        ]

    async def create_group_chat(self, connector, group_name, members):
        if connector.status != WeChatStatus.CONNECTED:
            return False, "Not connected", {}
        group = {
            "group_id": f"mock-group-{abs(hash(group_name)) % 100000}",
            "group_name": group_name,
            "members": members,
        }
        return True, "Group created (mock)", group


class OpenClawWeChatAdapter(BaseWeChatAdapter):
    mode_name = "openclaw"

    async def login(self, connector):
        if not OPENCLAW_AVAILABLE:
            connector.status = WeChatStatus.DISCONNECTED
            return False

        try:
            subprocess.run(
                [OPENCLAW_BIN, "--version"],
                check=True,
                capture_output=True,
                text=True,
            )
            connector.agent = {"type": "openclaw-cli", "bin": OPENCLAW_BIN}
            connector.status = WeChatStatus.CONNECTED
            return True
        except Exception:
            connector.status = WeChatStatus.DISCONNECTED
            return False

    async def send_message(self, connector, contact_name, text):
        if connector.status != WeChatStatus.CONNECTED:
            return False, "Not connected"
        if connector.agent is None or not OPENCLAW_AVAILABLE:
            return False, "OpenClaw runtime unavailable"
        return True, f"Queued for OpenClaw runtime: {contact_name}"

    async def list_contacts(self, connector):
        if connector.agent is None:
            return []
        return [
            {"name": "OpenClaw Inbox", "wechat_id": "openclaw-inbox"},
            {"name": "Operator Bridge", "wechat_id": "operator-bridge"},
        ]

    async def create_group_chat(self, connector, group_name, members):
        if connector.status != WeChatStatus.CONNECTED:
            return False, "Not connected", {}
        if connector.agent is None:
            return False, "OpenClaw runtime unavailable", {}
        group = {
            "group_id": f"openclaw-group-{abs(hash(group_name)) % 100000}",
            "group_name": group_name,
            "members": members,
            "bridge_mode": "cli",
        }
        return True, "Group queued for OpenClaw runtime", group


class PausedWeChatAdapter(BaseWeChatAdapter):
    mode_name = "paused"

    async def login(self, connector):
        connector.status = WeChatStatus.PAUSED
        return False

    async def send_message(self, connector, contact_name, text):
        connector.status = WeChatStatus.PAUSED
        return False, "WeChat is paused (development focus: file processing)"

    async def list_contacts(self, connector):
        connector.status = WeChatStatus.PAUSED
        return []

    async def create_group_chat(self, connector, group_name, members):
        connector.status = WeChatStatus.PAUSED
        return False, "WeChat is paused (development focus: file processing)", {}


class WeChatConnector:
    """Pluggable WeChat connector using mock or optional OpenClaw adapter."""

    def __init__(self, mode=None):
        self.mode = self._resolve_mode(mode)
        self.requested_mode = mode or os.getenv("WECHAT_CONNECTOR_MODE", "auto")
        self.status = WeChatStatus.DISCONNECTED
        self.agent = None
        self.message_handler = None
        self.adapter = self._build_adapter()

    def _resolve_mode(self, mode):
        # Global kill-switch: keep WeChat fully disabled during file-processing work.
        # Default is paused unless explicitly enabled.
        if os.getenv("AGENTFLOW_ENABLE_WECHAT", "0") != "1":
            return "paused"
        requested = mode or os.getenv("WECHAT_CONNECTOR_MODE", "auto")
        if requested == "auto":
            return "openclaw" if OPENCLAW_AVAILABLE else "mock"
        return requested

    def _build_adapter(self):
        if self.mode == "paused":
            return PausedWeChatAdapter()
        if self.mode == "openclaw" and OPENCLAW_AVAILABLE:
            return OpenClawWeChatAdapter()
        return MockWeChatAdapter()

    @property
    def adapter_mode(self):
        return self.adapter.mode_name

    @property
    def capabilities(self):
        if self.adapter_mode == "paused":
            return {
                "requested_mode": self.requested_mode,
                "resolved_mode": self.adapter_mode,
                "openclaw_available": OPENCLAW_AVAILABLE,
                "openclaw_bin": OPENCLAW_BIN,
                "paused": True,
                "supports_inbound_mock": False,
                "supports_outbound_send": False,
                "supports_contact_read": False,
                "supports_group_create": False,
            }
        return {
            "requested_mode": self.requested_mode,
            "resolved_mode": self.adapter_mode,
            "openclaw_available": OPENCLAW_AVAILABLE,
            "openclaw_bin": OPENCLAW_BIN,
            "supports_inbound_mock": True,
            "supports_outbound_send": True,
            "supports_contact_read": True,
            "supports_group_create": True,
        }

    def on_message(self, func):
        self.message_handler = func
        return func

    async def simulate_incoming_message(self, contact_name, text):
        await self.adapter.simulate_incoming_message(self, contact_name, text)

    async def login(self):
        if self.adapter_mode == "paused":
            self.status = WeChatStatus.PAUSED
            return False
        self.status = WeChatStatus.AUTHENTICATING
        return await self.adapter.login(self)

    async def send_message(self, contact_name, text):
        return await self.adapter.send_message(self, contact_name, text)

    async def list_contacts(self):
        return await self.adapter.list_contacts(self)

    async def create_group_chat(self, group_name, members):
        return await self.adapter.create_group_chat(self, group_name, members)

    async def receive_message(self, contact_name, text, metadata=None):
        payload = {"from": contact_name, "text": text}
        if metadata:
            payload["metadata"] = metadata
        if self.message_handler:
            await self.message_handler(payload)
            return True
        return False
