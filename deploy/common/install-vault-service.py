#!/usr/bin/env python3
"""Render persistent Vault settings and systemd units without exposing secrets."""
import argparse
import os
from pathlib import Path
import re
import secrets
import shlex
import tempfile


def write_private(path, content, mode=0o600):
    if path.is_symlink():
        raise ValueError(f"Refusing symlink: {path}")
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, temp = tempfile.mkstemp(dir=path.parent)
    try:
        os.fchmod(fd, mode)
        with os.fdopen(fd, 'w') as stream:
            stream.write(content)
        os.replace(temp, path)
    finally:
        if os.path.exists(temp):
            os.unlink(temp)


def read_env(path):
    out = {}
    if path.exists():
        for line in path.read_text().splitlines():
            if not line.strip() or line.lstrip().startswith('#'):
                continue
            key, sep, value = line.partition('=')
            if sep and re.fullmatch(r'[A-Z][A-Z0-9_]*', key):
                parts = shlex.split(value, comments=False)
                if len(parts) != 1:
                    if value.strip():
                        raise ValueError(f"Invalid setting: {key}")
                    out[key] = ''
                else:
                    out[key] = parts[0]
    return out


def render(app, docs, product, agent_port, vault_port, unit_dir):
    if not re.fullmatch(r'[a-z][a-z0-9-]*', product):
        raise ValueError('Invalid product name')
    for port in (agent_port, vault_port):
        if not 1024 <= port <= 65535:
            raise ValueError('Invalid port')
    if agent_port == vault_port:
        raise ValueError('Vault and agent require separate ports')
    for path in (app, docs, unit_dir):
        if not path.is_absolute() or any(c in str(path) for c in '\n\r%"\\$`'):
            raise ValueError('Unsafe service path')
    if app == docs or app.is_relative_to(docs):
        raise ValueError('Vault credential state must be outside workspace documents')
    values = read_env(app / '.env')
    # MVP audit storage is SQLite; collection may be explicitly disabled.
    provider = values.get('VAULT_AUDIT_PROVIDER', 'sqlite')
    if provider not in ('sqlite', 'off'):
        raise ValueError('VAULT_AUDIT_PROVIDER must be sqlite or off')
    state = app / 'state' / 'vault'
    state.mkdir(parents=True, exist_ok=True, mode=0o700)
    if state.resolve().is_relative_to(docs.resolve()):
        raise ValueError('Vault credential state resolves inside workspace documents')
    state.chmod(0o700)
    token = state / 'service-token'
    if token.is_symlink():
        raise ValueError('Vault service token cannot be a symlink')
    if not token.exists():
        write_private(token, secrets.token_hex(32))
    elif len(token.read_text().strip()) < 32:
        raise ValueError('Existing Vault service token is invalid')
    token.chmod(0o600)
    settings = {
        'LOCAL_MODE': 'false', 'GATEWAY_AUTH_MODE': 'platform',
        'GATEWAY_BIND': '127.0.0.1', 'GATEWAY_PORT': str(vault_port),
        'GATEWAY_PUBLIC_URL': f'http://127.0.0.1:{vault_port}',
        'GATEWAY_PRODUCT_URL': f'http://127.0.0.1:{agent_port}',
        'GATEWAY_STATE_DIR': str(state), 'GATEWAY_WORKSPACE_DIR': str(docs / 'Chats' / 'CapLayer'),
        'GATEWAY_HUMAN_TOKEN_FILE': str(token), 'GATEWAY_UPSTREAM_URL': 'none',
        'VAULT_AUDIT_PROVIDER': provider, 'VAULT_AUDIT_WRITE_MODE': values.get('VAULT_AUDIT_WRITE_MODE', 'async'),
    }
    for key, value in values.items():
        if key in ('VAULT_AUDIT_PROVIDER', 'VAULT_AUDIT_WRITE_MODE', 'VAULT_AUDIT_RETENTION', 'VAULT_AUDIT_MAX_MB'):
            if '\n' in value or '\r' in value:
                raise ValueError('Audit settings must be single line')
            settings[key] = value
    # Quoting compatible with both systemd EnvironmentFile and shell bootstrap.
    def quote(value):
        return '"' + value.replace('\\', '\\\\').replace('"', '\\"').replace('`', '\\`').replace('$', '\\$') + '"'
    write_private(state / 'service.env', ''.join(f'{key}={quote(value)}\n' for key, value in settings.items()))
    write_private(state / 'agent.env', f'CAPLAYER_SERVICE_URL=http://127.0.0.1:{vault_port}\nCAPLAYER_SERVICE_TOKEN_FILE="{token}"\n')
    unit = f'''[Unit]
Description={product} Vault MCP policy service
After=network-online.target
Before={product}-agent.service

[Service]
EnvironmentFile={state}/service.env
WorkingDirectory={app}/current
ExecStart="{app}/current/bin/agentworks-vault"
UMask=0077
NoNewPrivileges=true
Restart=on-failure
RestartSec=5
TimeoutStopSec=60

[Install]
WantedBy=default.target
'''
    write_private(unit_dir / f'{product}-vault.service', unit, 0o644)
    write_private(unit_dir / f'{product}-agent.service.d' / 'zz-vault.conf', f'[Unit]\nWants={product}-vault.service\nAfter={product}-vault.service\n\n[Service]\nEnvironmentFile={state}/agent.env\n', 0o644)
    return state


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('--app-dir', required=True, type=Path)
    parser.add_argument('--docs-dir', required=True, type=Path)
    parser.add_argument('--product', required=True)
    parser.add_argument('--agent-port', required=True, type=int)
    parser.add_argument('--vault-port', required=True, type=int)
    parser.add_argument('--unit-dir', type=Path, default=Path.home() / '.config/systemd/user')
    args = parser.parse_args()
    try:
        render(args.app_dir, args.docs_dir, args.product, args.agent_port, args.vault_port, args.unit_dir)
    except ValueError as error:
        parser.exit(1, f'Vault installation: {error}\n')
