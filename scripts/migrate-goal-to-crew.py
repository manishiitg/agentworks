#!/usr/bin/env python3
"""Convert ONE standalone Goal into a selected Crew, with the app stopped.

Default: inspect without writes. --apply: back up affected data, then convert.
Run as the app service account so new chat directories remain writable by it.
Create the destination Crew normally first (or choose an existing Crew). The
workflow and entry IDs remain stable. Authored code/definitions live in Crew
Files; runs, business data and history move into Crew private storage. No startup fallback or automatic installation-wide conversion.
"""
import argparse
from contextlib import closing
import importlib.util
import copy
from datetime import datetime, timezone
import hashlib
import json
from pathlib import Path
import re
import shutil
import sqlite3
import sys
import subprocess
import os
import tarfile
import tempfile
import uuid


def stamp():
    return datetime.now(timezone.utc).isoformat()


def read(path, default=None):
    if not path.exists():
        return copy.deepcopy(default)
    return json.loads(path.read_text())


def encode(value):
    return (json.dumps(value, indent=2, ensure_ascii=False) + '\n').encode()


def safe_id(value):
    if not isinstance(value, str) or not re.fullmatch(r'[A-Za-z0-9_-]{1,128}', value):
        raise ValueError('Choose an exact saved identity, without a path.')
    return value


def transient_name(name):
    return name in ('.tmp', '.cache', '.sandbox-cache') or name.startswith('.scratch-')


def migration_paths(root, skipped=None):
    """Never descend into disposable slot-owned caches, even to inspect them."""
    for directory, dirs, files in os.walk(root, followlinks=False):
        for name in sorted(dirs + files):
            child = Path(directory) / name
            if transient_name(name):
                if name in dirs:
                    dirs.remove(name)
                if skipped is not None:
                    skipped.append(child)
                continue
            yield child


def copy_migration_tree(source, target, skipped):
    def ignore(directory, names):
        ignored = [name for name in names if transient_name(name)]
        skipped.extend(str(Path(directory, name).relative_to(source)) for name in ignored)
        return ignored
    shutil.copytree(source, target, symlinks=True, ignore=ignore)


def no_links(path):
    for parent in [path, *path.parents]:
        if parent.is_symlink():
            raise ValueError(f'Symlink is not a migration source/destination: {parent}')
    if path.is_dir():
        for p in migration_paths(path):
            if p.is_symlink():
                raise ValueError(f'Resolve the symlink before converting: {p}')


class MigrationReadConnection(sqlite3.Connection):
    scratch = None

    def close(self):
        try:
            super().close()
        finally:
            if self.scratch:
                self.scratch.cleanup()


def db_read(path):
    # SQLite mode=ro can still create/update -wal/-shm beside a WAL database.
    # A cold database is immutable; committed WAL data is read from a private
    # copy instead. The stopped-installation precondition still applies.
    wal = path.with_name(path.name+'-wal')
    if not wal.exists() or wal.stat().st_size == 0:
        return sqlite3.connect(path.as_uri() + '?mode=ro&immutable=1', uri=True)
    scratch = tempfile.TemporaryDirectory(prefix='workflow-db-inspect-')
    try:
        copied = Path(scratch.name)/path.name
        shutil.copy2(path, copied)
        shutil.copy2(wal, copied.with_name(copied.name+'-wal'))
        connection = sqlite3.connect(copied.as_uri()+'?mode=ro', uri=True, factory=MigrationReadConnection)
        connection.scratch = scratch
        return connection
    except BaseException:
        scratch.cleanup()
        raise


def proven_workflow_owner(docs, source, logical):
    """PLAT-746-396: infer no identity from a machine-wide default."""
    registered, archived, callers = set(), set(), set()
    for file in (docs/'_users').glob('*/chat_history/product-conversations.json'):
        if any(row.get('workspace_path') == logical for row in read(file, {}).get('entries', {}).values()):
            registered.add(file.parents[1].name)
    for file in (source/'builder/conversation').rglob('*-conversation.json'):
        archived.add(read(file, {}).get('user_id'))
    for filename, field in [('schedules.json', 'schedules'), ('functions.json', 'functions'), ('webhooks.json', 'webhooks')]:
        for row in read(source/filename, {}).get(field, []):
            callers.add(row.get('run_as') or row.get('created_by'))
    if len(registered) != 1 or archived != registered or not callers.issubset(registered):
        raise ValueError('Missing workflow owner: chat, archive and entry evidence must identify one person; no owner was guessed.')
    owner = safe_id(next(iter(registered)))
    return owner, {'registered_people': sorted(registered), 'archive_people': sorted(archived), 'entry_people': sorted(callers)}


def promote_builder_main(document, manifest, conversation_key):
    """Main is a role pointer. Keep session IDs and all earlier chats intact."""
    fullkey = 'work/' + conversation_key
    current = document['entries'][fullkey]
    oldkey = 'work/' + (manifest.get('main_chat_key') or manifest['id'])
    if oldkey != fullkey and oldkey in document['entries']:
        prior = document.setdefault('previous', {}).setdefault(fullkey, [])
        old = document['entries'].pop(oldkey)
        history = [old, *document.get('previous', {}).pop(oldkey, [])]
        seen = {item.get('session_id') for item in [current, *prior]}
        for item in history:
            if item.get('session_id') and item['session_id'] not in seen:
                item['conversation_key'] = conversation_key
                prior.append(item)
                seen.add(item['session_id'])
        document.get('agents', {}).pop(oldkey, None)
    current['title'] = 'Main'
    document.setdefault('agents', {}).setdefault(fullkey, {})['name'] = 'Main'
    manifest['main_chat_key'] = conversation_key
    manifest['session_id'] = current['session_id']


def file_sha256(file):
    with file.open('rb') as stream:
        return hashlib.file_digest(stream,'sha256').hexdigest()


def tree_hashes(root):
    return {str(file.relative_to(root)): file_sha256(file)
            for file in migration_paths(root) if file.is_file() and not file.is_symlink()}


def verified_backup(backup, entries):
    """Backup must be outside the moving roots; verify bytes before any removal."""
    entries = [(label, Path(file)) for label, file in entries if file.exists()]
    for _, file in entries:
        no_links(file)
        if backup == file or file in backup.parents:
            raise ValueError('Choose a backup outside the active workflow storage.')
    required = sum(file.stat().st_size if file.is_file() else sum(p.stat().st_size for p in migration_paths(file) if p.is_file())
                   for _, file in entries)
    if shutil.disk_usage(backup.parent).free < required + (64 << 20):
        raise ValueError('Not enough free space for the complete workflow backup.')
    backup.mkdir(mode=0o700)
    expected, expected_links, skipped = {}, {}, []
    with tarfile.open(backup/'files.tar.gz', 'w:gz',compresslevel=1) as archive:
        for label, file in entries:
            archive.add(file, arcname=label, filter=lambda member: None if any(transient_name(part) for part in Path(member.name).parts) else member)
            if file.is_dir():
                expected.update({label+'/'+key: value for key, value in tree_hashes(file).items()})
                omitted = []
                list(migration_paths(file, omitted))
                skipped.extend(label+'/'+str(p.relative_to(file)) for p in omitted)
            else:
                expected[label] = file_sha256(file)
    observed, observed_links = {}, {}
    with tarfile.open(backup/'files.tar.gz', 'r:gz') as archive:
        for member in archive.getmembers():
            if member.isfile() or member.islnk():
                with archive.extractfile(member) as stream:
                    observed[member.name] = hashlib.file_digest(stream,'sha256').hexdigest()
            elif member.issym():
                observed_links[member.name] = member.linkname
    if expected != observed or expected_links != observed_links:
        raise ValueError('Backup verification failed. No workflow files were moved.')
    (backup/'sha256.json').write_bytes(encode(expected))
    if expected_links:
        (backup/'symlinks.json').write_bytes(encode(expected_links))
    (backup/'skipped.json').write_bytes(encode(sorted(skipped)))


def rewrite_physical_paths(value, source, runtime):
    # Update structured file references, never historical conversation text or
    # arbitrary substrings in prose, SQL or command bodies.
    if isinstance(value, str):
        if value == str(source) or value.startswith(str(source)+'/'):
            return str(runtime) + value[len(str(source)):]
        return value
    if isinstance(value, list):
        return [rewrite_physical_paths(item, source, runtime) for item in value]
    if isinstance(value, dict):
        return {key: item if key in ('conversation_history', 'messages', 'content', 'text', 'command', 'description', 'instruction', 'instructions', 'prompt', 'message', 'shell')
                else rewrite_physical_paths(item, source, runtime) for key, item in value.items()}
    return value


def update_runtime_references(source, runtime):
    changed = 0
    for file in migration_paths(runtime):
        if file.suffix != '.json' or not file.is_file():
            continue
        if 'conversation' in file.parts or file.name.endswith('-conversation.json'):
            continue
        try:
            original = read(file)
        except (ValueError, UnicodeError):
            continue
        revised = rewrite_physical_paths(original, source, runtime)
        if revised != original:
            file.write_bytes(encode(revised)); changed += 1
    return changed


def database_path_columns(source):
    """Report locations needing judgment; never return values or rewrite cells."""
    findings = []
    logical = '/Workflow/' + source.name
    needles = [str(source), logical+'/', logical.replace('/', '\\')+'\\']
    quote = lambda name: '"'+name.replace('"', '""')+'"'
    for file in sorted(migration_paths(source)):
        if file.suffix != '.sqlite' or not file.is_file():
            continue
        with closing(db_read(file)) as db:
            for (table,) in db.execute("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'").fetchall():
                for column in db.execute('PRAGMA table_info('+quote(table)+')').fetchall():
                    name = column[1]
                    checks = ' OR '.join('instr('+quote(name)+',?)>0' for _ in needles)
                    if db.execute('SELECT 1 FROM '+quote(table)+' WHERE typeof('+quote(name)+")='text' AND ("+checks+') LIMIT 1', needles).fetchone():
                        findings.append({'database':str(file.relative_to(source)), 'table':table, 'column':name})
    return findings


class RuntimeRelocation:
    """Finish one previously converted Crew workflow using its saved binding."""
    def __init__(self, docs, state, workflow, crew):
        self.docs, self.state = Path(docs).resolve(strict=True), Path(state).resolve(strict=True)
        self.root, self.crew = 'Workflow/'+safe_id(workflow), safe_id(crew)
        binding = read(self.state/'ownership/crew-executables'/(hashlib.sha256(self.root.encode()).hexdigest()+'.json'))
        if not binding or binding.get('kind') != 'workflow' or binding.get('workspace_path') != self.root or binding.get('crew_id') != crew:
            raise ValueError('Choose an existing workflow bound to this Crew.')
        self.workflow = safe_id(binding['id'])
        self.source = self.docs/self.root
        self.private = self.docs/'_system/crew-data'/crew
        self.runtime = self.private/'workflows'/self.workflow
        projects = read(self.state/'ownership/projects.json',{}).get('projects',{})
        matches = [p for p in projects.values() if p.get('project_id') == crew and p.get('product') == 'work']
        if len(matches) != 1 or binding['crew_root'] != 'Crew/'+safe_id(matches[0]['folder']):
            raise ValueError('Repair the owning Crew registration before moving files.')
        self.files = Path(matches[0].get('local_root') or self.docs/binding['crew_root'])
        self.definitions = self.files/'.agentworks/workflows'/self.workflow
        self.code = self.files/'.agentworks/code'/self.workflow
        self.receipt = self.state/'migrations/goal-to-crew'/(workflow+'-runtime.json')
        self.done = read(self.receipt)
        for file in (self.source,self.runtime,self.definitions,self.code,self.receipt):
            no_links(file)
        if self.done and not self.source.exists() and self.runtime.is_dir():
            return
        if not self.source.is_dir() or self.runtime.exists():
            raise ValueError('Expected one old runtime folder and an absent Crew runtime destination.')
        if not (self.definitions/'workflow.json').is_file():
            raise ValueError('The owning Crew must already contain this workflow definition.')
        self.database_path_columns = database_path_columns(self.source)
        # Partial source moves must be resolved explicitly; never choose a plan.
        for name in ('workflow.json','variables','code','planning/plan.json','planning/step_config.json'):
            file = self.source/name
            if file.is_file() or file.is_dir() and any(file.iterdir()):
                raise ValueError('Old authored source still exists; resolve the collision before moving: '+name)

    def report(self):
        return {'status':'already migrated' if self.done else 'ready', 'workflow':self.root, 'crew_id':self.crew,
                'runtime':str(self.runtime), 'files':len(tree_hashes(self.source)) if self.source.exists() else 0,
                'backup':self.done.get('backup') if self.done else None,
                'database_columns_with_old_paths':self.done.get('database_columns_with_old_paths', []) if self.done else self.database_path_columns}

    def apply(self):
        if self.done:
            return self.report()
        parent = self.state/'migrations/goal-to-crew'
        parent.mkdir(parents=True,exist_ok=True)
        backup = parent/('runtime-'+self.workflow+'-'+datetime.now(timezone.utc).strftime('%Y%m%dT%H%M%S%f'))
        verified_backup(backup,[('source',self.source),('definitions',self.definitions),('code',self.code),('destination-private',self.private)])
        self.runtime.parent.mkdir(parents=True,exist_ok=True)
        # Copy and check the final tree before removing the old source. This
        # deliberately runs offline; there is no concurrent-writer protocol.
        expected = tree_hashes(self.source)
        skipped = []
        try:
            copy_migration_tree(self.source,self.runtime,skipped)
            if tree_hashes(self.runtime) != expected:
                raise ValueError('Runtime copy verification failed; old files remain intact.')
        except BaseException:
            if self.runtime.exists():
                shutil.rmtree(self.runtime)
            raise
        # Caches contain installed packages/browser binaries. Move their roots
        # intact after verification, without inspecting their contents or links.
        # A failed rename stops before removing the old source.
        moved = []
        try:
            for relative in skipped:
                (self.source/relative).rename(self.runtime/relative)
                moved.append(relative)
        except BaseException as move_error:
            for relative in reversed(moved):
                try:
                    (self.runtime/relative).rename(self.source/relative)
                except BaseException as restore_error:
                    raise RuntimeError(
                        f'Cache move failed and could not restore {self.runtime/relative} '
                        f'to {self.source/relative}: {restore_error}. '
                        f'Preserve both {self.source} and {self.runtime}; do not delete either.'
                    ) from move_error
            shutil.rmtree(self.runtime)
            raise
        changed = update_runtime_references(self.source,self.runtime)
        shutil.rmtree(self.source)
        result=self.report();result.update(status='migrated',backup=str(backup),updated_references=changed,preserved_transient_roots=skipped,completed_at=stamp())
        self.receipt.write_bytes(encode(result))
        self.done=result
        return result


class Conversion:
    def __init__(self, docs, state, workflow, crew, agent_binary=None, split_schedule_groups=False, deployment_mode="local", leave_schedules=False):
        self.docs, self.state = Path(docs).resolve(strict=True), Path(state).resolve(strict=True)
        self.source = self.docs / 'Workflow' / safe_id(workflow)
        self.root = 'Workflow/' + workflow
        self.crew = safe_id(crew)
        self.writes, self.moves = {}, []
        self.agent_binary = Path(agent_binary).resolve() if agent_binary else None
        self.split_schedule_groups = split_schedule_groups
        self.leave_schedules = leave_schedules
        self.left_schedules = None
        self.owner_proof = None
        self.journal = self.state/"structured-chat-events-v2.sqlite"
        self.archives = {}
        self.chat_imports = []
        self.chat_history_checks = {}
        self.counts = {}
        self.binding_file = self.state / 'ownership/crew-executables' / (hashlib.sha256(self.root.encode()).hexdigest() + '.json')
        self.receipt = self.state / 'migrations/goal-to-crew' / (workflow + '.json')
        self.done = read(self.receipt)
        if self.done:
            if self.done['crew_id'] != crew:
                raise ValueError('This Goal has already moved to a different Crew.')
            return
        if self.binding_file.exists():
            raise ValueError('This workflow is already Crew-owned. Do not import it twice.')
        self.backup_skipped = []
        list(migration_paths(self.source, self.backup_skipped))
        no_links(self.source)
        self.manifest = read(self.source / 'workflow.json')
        if not self.manifest or self.manifest.get('kind'):
            raise ValueError('Choose a standalone Goal workflow, not a script or Relay.')
        if type(self.manifest.get('code_layout_version')) is not int or self.manifest['code_layout_version'] != 1:
            raise ValueError('Not upgraded: source layout 0 needs manual recreation. Definitions, files and timers were left unchanged.')
        self.workflow = safe_id(self.manifest['id'])
        for file in (self.state/'ownership/crew-executables').glob('*.json'):
            if read(file).get('id') == self.workflow:
                raise ValueError('This workflow ID is already registered to a Crew.')
        for file in (self.state/'ownership/crew-executables').glob('*.json'):
            binding = read(file)
            if binding.get('crew_id') == crew and binding.get('kind') == 'workflow':
                raise ValueError('Destination Crew already has a workflow; choose a Crew without one.')
        owners = read(self.state / 'ownership/workflows.json', {})
        saved_owner = owners.get(self.root)
        if not saved_owner:
            saved_owner, self.owner_proof = proven_workflow_owner(self.docs, self.source, self.root)
            owners[self.root] = saved_owner
            self.writes[self.state/'ownership/workflows.json'] = encode(owners)
        self.owner = safe_id(saved_owner)
        projects = read(self.state / 'ownership/projects.json', {}).get('projects', {})
        matches = [p for p in projects.values() if p.get('product') == 'work' and p.get('project_id') == crew]
        if len(matches) != 1:
            raise ValueError('Create or choose one registered destination Crew first.')
        self.project = matches[0]
        self.crew_root = 'Crew/' + safe_id(self.project['folder'])
        self.private = self.docs / '_system/crew-data' / crew
        self.runtime = self.private/'workflows'/self.workflow
        self.files = Path(self.project.get('local_root') or self.docs / self.crew_root)
        if not self.files.is_absolute():
            raise ValueError('Destination project folder must be absolute.')
        self.definitions = self.files / '.agentworks/workflows' / self.workflow
        self.code = self.files / '.agentworks/code' / self.workflow
        for p in [self.private, self.definitions, self.code, self.binding_file, self.receipt]:
            no_links(p)
        if not (self.private / 'product.json').is_file():
            raise ValueError('Destination Crew needs its current private storage layout first.')
        if self.definitions.exists() or self.code.exists() or self.runtime.exists():
            raise ValueError('Destination already has source for this workflow; resolve the collision first.')
        # Shared workflows move into a Crew through its Team (decisions log,
        # 2026-11-08): the Crew owner plus ACCEPTED Team members must cover
        # everyone with access today (owners, editors and Run people). This
        # deliberately gives former Run people the Crew's files and agents.
        # Pending invites do not count; the execution owner never changes.
        access = self.manifest.get('access') or {}
        source_owners = set(access.get('owners') or [self.owner])
        if self.owner != self.project['owner_id']:
            raise ValueError('Choose a Crew owned by this workflow\'s execution owner.')
        covered = {self.project['owner_id'], *self.project.get('co_owners', [])}
        team = read(self.state/'team-folders'/crew/'team.json')
        if team:
            if team.get('project_id') != crew or team.get('owner_id') != self.project['owner_id']:
                raise ValueError('Repair the destination Crew Team ownership record before converting.')
            if team.get('on'):
                covered.update(person['user_id'] for person in team.get('people', []) if not person.get('invited'))
        if access.get('run_everyone'):
            raise ValueError('This workflow is shared with everyone. A Crew Team lists people by name: narrow the workflow to named people, or add them to the Crew Team, then convert.')
        needed = source_owners | set(access.get('editors') or []) | set(access.get('readers') or [])
        missing = sorted(needed - covered)
        if missing:
            raise ValueError('Add these people to the destination Crew Team and have them accept first: ' + ', '.join(missing))
        if self.manifest.get('team') or self.project.get('shared_agents'):
            raise ValueError('Resolve Team permissions or individually shared agents before converting this Goal.')
        sync = self.state / 'crew-sync' / (crew + '.json')
        if sync.exists():
            raise ValueError('Convert an unpaired destination first; publishing/sync conversion is separate.')
        # Editors built the workflow too; their Builder chats move with it.
        self.owners = source_owners | set(access.get('editors') or [])
        self.readers = set(access.get("readers") or [])
        self.chat_keys = {}
        self.sessions = {}
        self.prepare_left_schedules()
        self.prepare_legacy()
        self.prepare_manifest_locations(projects, deployment_mode)
        self.chats()
        self.chat_journal()
        self.prepare_saved_groups()
        self.entries()
        self.prepare_input_contracts()
        self.inputs()
        self.inspect_database()
        self.source_moves()
        self.writes[self.binding_file] = encode({'id': self.workflow, 'crew_id': crew, 'crew_root': self.crew_root,
                                                 'workspace_path': self.root, 'kind': 'workflow', 'runtime_root': 'workflows/' + self.workflow})
        self.counts.update(workflows=1, goals=0,
                           historical_runs=len(list((self.source / 'runs').glob('*'))),
                           dashboards=len(list((self.source / 'db/reports').glob('*.html'))))

    def document(self, path, default=None):
        return json.loads(self.writes[path]) if path in self.writes else read(path, default)

    def prepare_left_schedules(self):
        if not self.leave_schedules:
            return
        if self.split_schedule_groups:
            raise ValueError('Choose leaving schedules or splitting schedules, not both.')
        source = self.source/'schedules.json'
        if not source.exists():
            if self.manifest.get('schedules'):
                raise ValueError('Leaving schedules needs the separate schedules.json store first.')
            return
        no_links(source)
        raw = source.read_bytes()
        rows = json.loads(raw).get('schedules')
        if not isinstance(rows, list) or any(not isinstance(row, dict) for row in rows):
            raise ValueError('Schedules need a list of saved timer records.')
        enabled = [str(row.get('id', '(missing ID)')) for row in rows if row.get('enabled') is not False]
        if enabled:
            raise ValueError('Leaving schedules requires already-disabled timers; unchanged IDs: ' + ', '.join(enabled))
        target = self.private/'migration-archives/workflow-schedules'/self.workflow/'schedules.json'
        self.move(source, target)
        self.left_schedules = {'path':str(target), 'bytes':len(raw), 'sha256':hashlib.sha256(raw).hexdigest(),
                               'timer_ids':[row.get('id') for row in rows], 'registered':False}

    def prepare_legacy(self):
        # Reuse the existing one-off entry/input converters, scoped to a
        # disposable copy of ONE Goal. No other Goal is scanned or modified.
        def module(name):
            spec = importlib.util.spec_from_file_location(name, Path(__file__).with_name(name+'.py'))
            value = importlib.util.module_from_spec(spec); spec.loader.exec_module(value)
            return value
        with tempfile.TemporaryDirectory(prefix='goal-conversion-') as directory:
            stage = Path(directory)
            source = stage/self.root
            source.mkdir(parents=True)
            for relative in ['workflow.json', 'schedules.json', 'functions.json', 'webhooks.json', 'variables/variables.json']:
                if self.leave_schedules and relative == 'schedules.json':
                    continue
                file = self.source/relative
                if file.exists():
                    target = source/relative; target.parent.mkdir(parents=True, exist_ok=True)
                    shutil.copy2(file, target)
            if self.leave_schedules:
                (source/'schedules.json').write_bytes(encode({'schedules':[]}))
            timer_module = module('migrate-project-schedules')
            timer_changes, counts = timer_module.inspect(stage, self.owner)
            entry_changes = timer_module.entry_changes(stage, timer_changes, counts, self.owner)
            for file, value in entry_changes:
                file.parent.mkdir(parents=True, exist_ok=True); file.write_bytes(encode(value))
            for file in source.rglob('*'):
                if file.is_file():
                    target = self.source/file.relative_to(source)
                    if self.leave_schedules and target == self.source/'schedules.json':
                        continue
                    if not target.exists() or file.read_bytes() != target.read_bytes():
                        self.writes[target] = file.read_bytes()
            self.manifest = self.document(self.source/'workflow.json')

    def prepare_manifest_locations(self, projects, deployment_mode):
        # PLAT-746-396: paths saved before a target Crew move are not identities.
        # Resolve the exact registered target; never widen a shared connection.
        manifest = self.document(self.source/'workflow.json')
        changes = 0
        for attachment in manifest.get('crew_attachments') or []:
            profile = attachment.get('crew_profile_id') or 'work'
            if profile == 'crewx':
                profile = 'work'
            target_id = safe_id(attachment.get('crew_project_id'))
            matches = [p for p in projects.values() if p.get('product') == profile and p.get('project_id') == target_id]
            if len(matches) != 1:
                raise ValueError('Crew connection target is missing or ambiguous; restore it or choose its intended connection before converting. Nothing was changed.')
            target = matches[0]
            if target.get('owner_id') != self.owner:
                raise ValueError('You do not own a Crew connection target; ask its owner to repair the connection before converting. Nothing was changed.')
            root = 'Crew/' + safe_id(target['folder'])
            if target.get('canonical_root') not in (None, '', root):
                raise ValueError('Repair the registered Crew connection location before converting.')
            if attachment.get('crew_workspace_path') != root:
                attachment['crew_workspace_path'] = root
                changes += 1
        # Reuse the original PLAT-788 policy, rather than invent a new default.
        browser_file = Path(__file__).with_name('migrate-browser-modes.py')
        spec = importlib.util.spec_from_file_location('browser_modes', browser_file)
        browser = importlib.util.module_from_spec(spec); spec.loader.exec_module(browser)
        caps = manifest.get('capabilities') or {}
        mode = caps.get('browser_mode')
        if mode:
            converted = browser.convert(mode, deployment_mode)
            if converted != mode:
                caps['browser_mode'] = converted
                manifest['capabilities'] = caps
                self.counts['browser_modes_refreshed'] = 1
        self.counts['crew_attachment_locations_refreshed'] = changes
        if manifest != self.document(self.source/'workflow.json'):
            self.writes[self.source/'workflow.json'] = encode(manifest)
        self.manifest = manifest

    def prepare_input_contracts(self):
        # Entries first convert old groups to one preset. Validate the final
        # candidate, not the legacy input envelope which runtime refuses.
        spec = importlib.util.spec_from_file_location('workflow_inputs', Path(__file__).with_name('migrate-workflow-input-contracts.py'))
        converter = importlib.util.module_from_spec(spec); spec.loader.exec_module(converter)
        with tempfile.TemporaryDirectory(prefix='goal-input-conversion-') as directory:
            stage = Path(directory)
            for relative in ['workflow.json', 'functions.json', 'variables/variables.json']:
                target = stage/relative
                target.parent.mkdir(parents=True, exist_ok=True)
                default = {'variables': []} if relative.startswith('variables/') else {}
                target.write_bytes(encode(self.document(self.source/relative, default)))
            changes, errors = converter.convert(stage, self.agent_binary)
            if errors:
                raise ValueError('Input conversion needs an author decision: ' + '; '.join(errors))
            for file, value in changes.items():
                self.write_json(self.source/file.relative_to(stage), value)

    def prepare_saved_groups(self):
        spec = importlib.util.spec_from_file_location('workflow_saved_groups', Path(__file__).with_name('migrate-workflow-input-contracts.py'))
        converter = importlib.util.module_from_spec(spec); spec.loader.exec_module(converter)
        definitions = self.document(self.source/'variables/variables.json', {})
        active = [g['name'] for g in definitions.get('groups', []) if g.get('enabled') is True]
        with tempfile.TemporaryDirectory(prefix='goal-saved-groups-') as directory:
            stage = Path(directory)
            for relative in ['workflow.json', 'schedules.json', 'functions.json', 'webhooks.json', 'variables/variables.json']:
                if self.leave_schedules and relative == 'schedules.json':
                    continue
                value = self.document(self.source/relative)
                if value is None:
                    continue
                if relative in ('schedules.json', 'functions.json', 'webhooks.json'):
                    for entry in value.get(relative[:-5], []):
                        inputs = entry.setdefault('input', {})
                        if entry.get('action', {}).get('type') == 'run' and not inputs.get('input_preset') and not inputs.get('groups') and active:
                            inputs['groups'] = list(active)
                target = stage/relative; target.parent.mkdir(parents=True, exist_ok=True)
                target.write_bytes(encode(value))
            changes, errors = converter.convert_saved_groups(stage, self.split_schedule_groups)
            if errors:
                raise ValueError('Saved entries need a decision: ' + '; '.join(errors))
            for file, value in changes.items():
                self.write_json(self.source/file.relative_to(stage), value)

    def write_json(self, path, value):
        self.writes[path] = encode(value)

    def move(self, source, target):
        if not source.exists():
            return
        no_links(source); no_links(target)
        if target.exists():
            raise ValueError(f'Destination already exists: {target.name}')
        self.moves.append((source, target))

    def chats(self):
        """Move registry identities, never concatenate another person's transcript."""
        for file in sorted((self.docs / '_users').glob('*/chat_history/product-conversations.json')):
            no_links(file)
            user = file.parents[1].name
            doc = read(file); changed = False
            primary = None
            if user == self.owner:
                candidates = [key for key, row in doc.get('entries', {}).items() if row.get('workspace_path') == self.root]
                owner_key = 'agentworks/' + self.workflow + ':chat:owner-' + hashlib.sha256(user.encode()).hexdigest()[:16]
                primary = next((key for key in [owner_key, 'agentworks/' + self.workflow] if key in candidates), None)
                if primary is None and len(candidates) == 1:
                    primary = candidates[0]
                if candidates and primary is None:
                    raise ValueError('Choose the owner’s primary Builder chat before conversion.')
            main_key = None
            for key, row in list(doc.get('entries', {}).items()):
                if row.get('workspace_path') != self.root:
                    continue
                if row.get('profile_id') != 'agentworks':
                    raise ValueError('A source chat has an unexpected profile; resolve it before conversion.')
                session = row.get('session_id')
                if not session or session in self.sessions:
                    raise ValueError('A source session is missing or appears in multiple chat registries.')
                digest = hashlib.sha256((user + '\0' + key).encode()).hexdigest()[:20]
                tab = 'imported-' + digest
                newkey = self.crew + ':chat:' + tab
                fullkey = 'work/' + newkey
                if fullkey in doc['entries']:
                    raise ValueError('Destination chat identity already exists.')
                self.chat_keys[(user, row['conversation_key'])] = tab
                self.sessions[session] = (user, newkey)
                for previous in doc.get('previous', {}).get(key, []):
                    if previous.get('session_id'):
                        self.sessions[previous['session_id']] = (user, newkey)
                for section in ['entries', 'previous', 'agents']:
                    value = doc.get(section, {}).pop(key, None)
                    if value is None:
                        continue
                    if section != 'agents':
                        for item in value if isinstance(value, list) else [value]:
                            item.update(profile_id='work', profile_version=1, conversation_key=newkey,
                                        workspace_path=self.crew_root, resource_id=self.crew, fresh_native=True)
                    doc.setdefault(section, {})[fullkey] = value
                if key == primary:
                    main_key = newkey
                # Other imported chats retain their own identities and names.
                doc.setdefault('agents', {}).setdefault(fullkey, {'name': row.get('title') or self.manifest.get('label') or self.workflow})
                changed = True
            if changed:
                if main_key:
                    manifest_path = self.private/'product.json'
                    manifest = self.document(manifest_path)
                    promote_builder_main(doc, manifest, main_key)
                    self.write_json(manifest_path, manifest)
                self.write_json(file, doc)
        source_files = list((self.source / 'builder/conversation').rglob('*-conversation.json'))
        self.counts['unregistered_chat_archives'] = 0
        for file in source_files:
            record = read(file)
            session, user = record.get('session_id'), record.get('user_id')
            if session not in self.sessions and isinstance(session,str) and session.startswith(('schedule-', 'sched_', 'bot-')):
                # Automated run/bot logs keep their existing runtime URLs and
                # attribution; they are not private human Builder tabs.
                continue
            if session not in self.sessions:
                # PLAT-746-364: older owner archives may predate the personal
                # registry. Keep their bytes in private workflow records; do
                # not invent live tabs or concatenate them into another chat.
                if user not in self.owners:
                    raise ValueError('An unregistered Builder transcript needs its private owner mapping repaired first.')
                self.counts['unregistered_chat_archives'] += 1
                continue
            if self.sessions[session][0] != user:
                raise ValueError('A Builder transcript lacks a matching personal chat registry, including previous sessions. Repair its owner/tab first.')
            # Keep each person's partition and actual date, even for co-owners.
            day = file.parent.name
            if not re.fullmatch(r'\d{4}-\d{2}-\d{2}', day):
                raise ValueError('A Builder transcript needs its dated storage path repaired.')
            if user not in self.owners:
                raise ValueError('A non-owner Builder transcript needs its private Run-chat mapping repaired first.')
            target = self.private / 'builder/conversation/users' / safe_id(user) / day / file.name
            self.move(file, target)
            # The JSON header's history_log points at this canonical append-only
            # sibling (workspace/chatlog.HistoryLogPath), not a disposable cache.
            history, history_target = file.with_suffix('.history.jsonl'), target.with_suffix('.history.jsonl')
            self.move(history, history_target)
            if history.exists():
                self.chat_history_checks[history_target] = (history.stat().st_size, hashlib.sha256(history.read_bytes()).hexdigest())
            # Preserve every message; only routing metadata changes.
            for key in ['workspace_path', 'workflow_workspace_path']:
                if record.get(key) == self.root:
                    record[key] = self.crew_root
            if record.get('agent_profile_id') == 'agentworks':
                record['agent_profile_id'] = 'work'
            self.write_json(target, record)
            self.remember_archive(session, user, target, record)
            # Derived resume snapshots are moved alongside the canonical archive.
            snapshot = file.with_name(file.name.replace('-conversation.json', '-resume.json'))
            self.move(snapshot, target.with_name(snapshot.name))
            if snapshot.exists():
                resume = read(snapshot)
                if resume.get('workspace_path') == self.root:
                    resume['workspace_path'] = self.crew_root
                self.write_json(target.with_name(snapshot.name), resume)
        # Private Run chats stay private. Patch only routing metadata for the
        # exact registered person's sessions, including compact resume files.
        for file in (self.docs/'_users').glob('*/chat_history/*/*.json'):
            if not file.name.endswith(('-conversation.json', '-resume.json')):
                continue
            record = read(file)
            identity = self.sessions.get(record.get('session_id'))
            if not identity:
                continue
            if file.parents[2].name != identity[0] or record.get('user_id') != identity[0]:
                raise ValueError('A private transcript has conflicting ownership; repair it first.')
            for key in ['workspace_path', 'workflow_workspace_path']:
                if record.get(key) == self.root:
                    record[key] = self.crew_root
            self.write_json(file, record)
            if file.name.endswith('-conversation.json'):
                self.remember_archive(record['session_id'], identity[0], file, record)
        # Update central history indexes by exact session identity, not string
        # replacement through chat text, instructions, URLs or arbitrary code.
        path_map = {str(a.relative_to(self.docs)): self.crew_root + '/' + str(b.relative_to(self.private)) for a, b in self.moves}
        for file in [*(self.docs / '_users').glob('*/chat_history/chat-index.json'), *(self.docs / '_users').glob('*/chat_history/index.json')]:
            doc = read(file); changed = False
            entries = doc.get('entries', {})
            for oldpath, row in list(entries.items()):
                session = row.get('session', {})
                if session.get('session_id') not in self.sessions:
                    continue
                if session.get('workspace_path') == self.root:
                    session['workspace_path'] = self.crew_root; changed = True
                if row.get('workspace_path') == self.root:
                    row['workspace_path'] = self.crew_root; changed = True
                if oldpath in path_map:
                    entries[path_map[oldpath]] = entries.pop(oldpath); changed = True
                for key in ['conversation_path', 'file_path', 'path']:
                    for metadata in [row, session]:
                        if metadata.get(key) in path_map:
                            metadata[key] = path_map[metadata[key]]; changed = True
            if changed:
                self.write_json(file, doc)
        for session, (user, _) in self.sessions.items():
            pin = self.state/'code-session-pins'/(hashlib.sha256(('code-session\0'+session).encode()).hexdigest()[:32]+'.json')
            saved = read(pin)
            if saved:
                if saved.get('person') != user or saved.get('code_root') != self.root:
                    raise ValueError('A migrated session is pinned to another person/project; repair it first.')
                saved['code_root'] = self.crew_root
                self.write_json(pin, saved)
        self.counts['chats'] = len(self.sessions)

    def remember_archive(self, session, user, file, record):
        rank = (record.get('updated_at') or '', str(file))
        existing = self.archives.get(session)
        if existing is None or rank > existing[0]:
            self.archives[session] = (rank, {'session_id':session,'owner_id':user,'archive':str(file)})

    def chat_journal(self):
        # Preserve canonical event IDs/cost associations when a journal exists.
        # JSON-only older chats need one explicit import for the current UI.
        no_links(self.journal)
        existing = {}
        if self.journal.exists():
            with closing(db_read(self.journal)) as db:
                owners = dict(db.execute('SELECT session_id,owner_id FROM structured_chat_sessions'))
                existing = dict(db.execute('SELECT session_id,last_sequence FROM structured_chat_sequences'))
                for session,(user,_) in self.sessions.items():
                    if owners.get(session) not in (None, user):
                        raise ValueError('A chat journal belongs to another person; repair its attribution first.')
        self.chat_imports = [item for session,(_,item) in self.archives.items() if existing.get(session,0)==0]
        self.counts['chat_archives_to_import'] = len(self.chat_imports)

    def destination(self, value, user):
        if not value or value == 'main':
            # Explicit saved source tab when one exists; otherwise Crew Main.
            source_main = self.workflow + ':chat:owner-' + hashlib.sha256(user.encode()).hexdigest()[:16]
            return self.chat_keys.get((user, source_main), 'main')
        full = value if ':chat:' in value else self.workflow + ':chat:' + value
        if (user, full) not in self.chat_keys:
            raise ValueError('An automation targets an unknown chat. Repair that saved destination first.')
        return self.chat_keys[(user, full)]

    def entries(self):
        definitions = self.document(self.source/'variables/variables.json', {})
        active_presets = [g['name'] for g in definitions.get('groups', []) if g.get('enabled') is True]
        if self.manifest.get('schedules'):
            raise ValueError('Convert legacy manifest schedules/functions to the current separate stores first.')
        for name, field in [('schedules.json','schedules'), ('functions.json','functions'), ('webhooks.json','webhooks')]:
            if self.leave_schedules and field == 'schedules':
                self.counts[field] = 0
                continue
            file = self.source / name
            doc = self.document(file, {field: []})
            for entry in doc[field]:
                user = entry.get('run_as') or entry.get('created_by') or self.owner
                if user != self.owner:
                    raise ValueError('An entry runs as another person. Choose its intended execution owner before converting.')
                action = entry.get('action') or {}
                if action.get('type') not in ('run', 'message'):
                    raise ValueError('An entry has an unsupported action.')
                action['tab'] = self.destination(action.get('tab'), user)
                if field == 'functions' and action.get('conversation') in ('isolated', 'fresh'):
                    action['conversation'] = 'new'
                if field == 'functions' and entry.get('execution') == 'fresh':
                    entry['execution'] = 'new'
                if entry.get('input_mapping') == {}:
                    entry.pop('input_mapping')
                entry['action'] = action
                values = entry.get('input') or {}
                groups = values.pop('groups', None)
                if groups:
                    if len(groups) != 1 or values.get('input_preset'):
                        raise ValueError('Split batch schedules into one saved input selection per entry before conversion.')
                    values['input_preset'] = groups[0]
                if action['type'] == 'run' and not values.get('input_preset') and active_presets:
                    if len(active_presets) != 1:
                        raise ValueError('This entry used an enabled group batch. Choose one input_preset per entry before converting.')
                    values['input_preset'] = active_presets[0]
                entry['input'] = values
                if entry.get('project') and entry['project'] != {'kind':'goal','id':self.workflow}:
                    raise ValueError('An entry points to another executable; repair it before conversion.')
                # Stable execution ID keeps function callers, hooks, scheduler
                # receipts and business data addressing the same workflow.
            if file.exists() or file in self.writes:
                self.write_json(file, doc)
            self.counts[field] = len(doc[field])

    def inputs(self):
        file = self.source / 'variables/variables.json'
        variables = self.document(file)
        if variables:
            for group in variables.get('groups', []):
                group.pop('enabled', None)
            self.write_json(file, variables)
        for name in ['functions.json', 'webhooks.json']:
            doc = json.loads(self.writes.get(self.source/name, b'{}'))
            for entry in doc.get(name[:-5], []):
                if entry.get('input_schema') and entry.get('action', {}).get('type') == 'run' and not entry.get('input_mapping'):
                    raise ValueError('Convert copied function input schemas with migrate-workflow-input-contracts.py first.')

    def inspect_database(self):
        # Historical tracking is ordinary data. Never rename IDs, move tables,
        # rewrite evidence or manufacture a retired Crew-goal object.
        self.source_db = self.source / 'db/db.sqlite'
        self.target_db = self.private / 'db/db.sqlite'
        no_links(self.source_db); no_links(self.target_db)
        self.database_path_columns = database_path_columns(self.source)
        self.counts.update(measurements=0, observations=0)
        if self.source_db.exists():
            with closing(db_read(self.source_db)) as db:
                for table, count in [('workflow_goal_metrics', 'measurements'), ('goal_observations', 'observations')]:
                    if db.execute("SELECT 1 FROM sqlite_master WHERE type='table' AND name=?", (table,)).fetchone():
                        self.counts[count] = db.execute(f'SELECT COUNT(*) FROM {table}').fetchone()[0]

    def source_moves(self):
        for name in ['workflow.json', 'variables']:
            self.move(self.source/name, self.definitions/name)
        for item in (self.source/'planning').glob('*'):
            if item.name not in ['changelog', 'revisions']:
                self.move(item, self.definitions/'planning'/item.name)
        for item in (self.source/'code').glob('*/script_metadata.json'):
            self.move(item, self.source/'runs/script-metadata'/(item.parent.name+'.json'))
        self.move(self.source/'code', self.code)
        self.move(self.source, self.runtime)
        destinations = sorted([*self.moves, (self.source/'variables', self.definitions/'variables')], key=lambda pair:len(pair[0].parts), reverse=True)
        for file in list(self.writes):
            for source,target in destinations:
                if file == source or source in file.parents:
                    self.writes[target/file.relative_to(source)] = self.writes.pop(file)
                    break

    def report(self):
        if self.done:
            return {'status':'already migrated', 'crew_id':self.crew, 'workflow':self.root, 'backup':self.done['backup']}
        return {'status':'ready', 'workflow':self.root, 'crew_id':self.crew,
                'left_schedules':self.left_schedules,
                'counts':self.counts, 'moves':[{'from':str(a.relative_to(self.docs)), 'to':str(b)} for a,b in self.moves],
                'preserved':['workflow identity','entry IDs and disabled state','run history and costs','business database','skills and knowledge','dashboard files and workflow-scoped links','secret references'],
                'historical_run_note':'Old run folders remain readable; start a new run instead of resuming a retired grouped run.',
                'historical_chat_note':'Unregistered owner chats remain unchanged in private workflow records; no chat tabs are created for them.',
                'needs_chat_import_binary':bool(self.chat_imports),
                'next':('Start the upgraded app and test this Python automation from its Crew chat.' if getattr(self, 'python_source', False) else 'Ask this Crew’s chat to update this workflow before running it. Task text and code were not rewritten.'),
                'owner_evidence':self.owner_proof,
                'carried_crew_links':getattr(self, 'carried_crew_links', []),
                'dashboard_retargets':getattr(self, 'dashboard_retargets', []),
                'dashboard_source_moves':getattr(self, 'dashboard_source_moves', []),
                'database_columns_with_old_paths':self.database_path_columns,
                'backup_skipped':[str(p.relative_to(self.source)) for p in self.backup_skipped],
                'chat_map':[{'person':user,'from':old,'to':tab} for (user,old),tab in self.chat_keys.items()]}

    def apply(self, backup=None, plain_backup=False):
        if self.done:
            return self.report()
        if self.chat_imports and (not self.agent_binary or not self.agent_binary.is_file() or not os.access(self.agent_binary, os.X_OK)):
            raise ValueError('This Goal has older JSON-only chats. Pass --agent-binary with this refactor build to import their visible history.')
        # The operator stops this installation's writers first. A plain backup
        # is sufficient; this is not an online migration/rollback coordinator.
        backup = Path(backup).resolve() if backup else self.state/'migrations/goal-to-crew'/('backup-'+self.workflow+'-'+datetime.now(timezone.utc).strftime('%Y%m%dT%H%M%S%f'))
        if plain_backup:
            backup = backup / self.workflow
            if backup.exists() and (not backup.is_dir() or any(backup.iterdir())):
                raise ValueError('This workflow backup already contains a snapshot. Keep it and choose a new backup folder.')
        for root in (self.source, self.private, self.definitions, self.code, *(target for _, target in self.moves)):
            if backup == root or root in backup.parents or backup in root.parents:
                raise ValueError('Choose a backup outside the active workflow and destination storage.')
        backup.parent.mkdir(parents=True, exist_ok=True)
        try:
            metadata_paths = {}
            entries = [('source',self.source),('destination-private',self.private)]
            for i,file in enumerate(self.writes):
                if file.exists() and self.source not in file.parents and self.private not in file.parents:
                    label='metadata/'+str(i);entries.append((label,file));metadata_paths[label]=str(file)
            if plain_backup:
                skipped = []
                for suffix in ('', '-wal', '-shm'):
                    file = self.journal.with_name(self.journal.name + suffix)
                    if file.exists():
                        entries.append(('chat-journal/' + file.name, file))
                backup.mkdir(parents=True, exist_ok=True, mode=0o700)
                for label, file in entries:
                    if not file.exists():
                        continue
                    if backup == file or file in backup.parents:
                        raise ValueError('Choose a backup outside the active workflow storage.')
                    target = backup/label
                    target.parent.mkdir(parents=True, exist_ok=True)
                    if file.is_dir():
                        omitted = []
                        copy_migration_tree(file, target, omitted)
                        skipped.extend(label+'/'+name for name in omitted)
                    else:
                        shutil.copy2(file, target)
                (backup/'skipped.json').write_bytes(encode(sorted(skipped)))
            else:
                verified_backup(backup,entries)
            # Snapshot DBs through SQLite, including WAL contents.
            for label, file in [('source', self.source_db), ('destination', self.target_db), ('chat-journal', self.journal)]:
                if file.exists() and not plain_backup:
                    with closing(db_read(file)) as src, closing(sqlite3.connect(backup/(label+'.sqlite'))) as dst:
                        src.backup(dst)
            (backup/'plan.json').write_bytes(encode(self.report()))
            (backup/'metadata-paths.json').write_bytes(encode(metadata_paths))
            for source,target in self.moves:
                target.parent.mkdir(parents=True, exist_ok=True)
                shutil.move(str(source), str(target))
            if self.left_schedules:
                archived = Path(self.left_schedules['path']).read_bytes()
                if len(archived) != self.left_schedules['bytes'] or hashlib.sha256(archived).hexdigest() != self.left_schedules['sha256']:
                    raise ValueError('Left schedules differ from their original snapshot.')
            for history, (size, digest) in self.chat_history_checks.items():
                if history.stat().st_size != size or hashlib.sha256(history.read_bytes()).hexdigest() != digest:
                    raise ValueError('Moved chat history differs from its original snapshot.')
            # Binding last: the next start sees only the canonical authored paths.
            for file,data in sorted(self.writes.items(), key=lambda kv: kv[0] == self.binding_file):
                file.parent.mkdir(parents=True, exist_ok=True)
                file.write_bytes(data)
            if self.chat_imports:
                plan = backup/'chat-imports.json'
                plan.write_bytes(encode(self.chat_imports))
                subprocess.run([str(self.agent_binary),'server','import-goal-chats','--plan',str(plan),'--journal',str(self.journal)],check=True,capture_output=True)
            # Learning files stay until the owning Crew upgrades; the one-off
            # post-stamp cleanup backs them up and removes them separately.
            references = update_runtime_references(self.source,self.runtime)
            assert not self.source.exists(), 'Old runtime unexpectedly recreated'
            result = self.report(); result.update(updated_references=references,status='migrated', backup=str(backup), completed_at=stamp())
            self.receipt.parent.mkdir(parents=True, exist_ok=True)
            self.receipt.write_bytes(encode(result))
            return result
        except Exception as exc:
            raise RuntimeError(f'Conversion stopped. Keep this instance stopped; restore affected files from {backup} before retrying. {type(exc).__name__}') from exc


class LearningCleanup:
    """One-off cleanup after the owning Crew has completed its contract upgrade."""
    def __init__(self, docs, state, workflow, crew):
        self.docs, self.state = Path(docs).resolve(strict=True), Path(state).resolve(strict=True)
        self.root = 'Workflow/' + safe_id(workflow)
        self.crew = safe_id(crew)
        binding = read(self.state/'ownership/crew-executables'/(hashlib.sha256(self.root.encode()).hexdigest()+'.json'), {})
        if binding.get('crew_id') != crew or binding.get('workspace_path') != self.root or binding.get('kind') != 'workflow':
            raise ValueError('Choose the exact workflow bound to this Crew.')
        workflow_id = safe_id(binding.get('id'))
        projects = read(self.state/'ownership/projects.json', {}).get('projects', {})
        matches = [p for p in projects.values() if p.get('product') == 'work' and p.get('project_id') == crew]
        if len(matches) != 1:
            raise ValueError('One registered destination Crew is required.')
        project = matches[0]
        files = Path(project.get('local_root') or self.docs/'Crew'/safe_id(project['folder']))
        if not files.is_absolute():
            raise ValueError('Destination project folder must be absolute.')
        self.manifest = files/'.agentworks/workflows'/workflow_id/'workflow.json'
        self.learning = self.docs/'_system/crew-data'/crew/'workflows'/workflow_id/'learnings'
        no_links(self.manifest); no_links(self.learning)

    def report(self):
        manifest = read(self.manifest, {})
        ready = manifest.get('version') == '1.0.52' and manifest.get('code_layout_version') == 1
        return {'status': 'not upgraded' if not ready else 'cleanup ready' if self.learning.exists() else 'already clean',
                'workflow': self.root, 'crew_id': self.crew, 'contract_version': manifest.get('version'),
                'next': 'Ask this Crew’s chat to update this workflow.' if not ready else 'Back up and remove retired learnings only; code and history stay unchanged.'}

    def apply(self):
        result = self.report()
        if result['status'] != 'cleanup ready':
            return result
        backup = self.state/'migrations/goal-to-crew'/('learnings-'+safe_id(self.root.split('/')[1])+'-'+datetime.now(timezone.utc).strftime('%Y%m%dT%H%M%S%f'))
        backup.parent.mkdir(parents=True, exist_ok=True)
        verified_backup(backup, [('learnings', self.learning), ('workflow.json', self.manifest)])
        shutil.rmtree(self.learning)
        result.update(status='cleaned', backup=str(backup))
        return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--docs', type=Path, required=True)
    parser.add_argument('--state', type=Path, required=True)
    parser.add_argument('--workflow', required=True, help='One source folder name under Workflow/ (not a display name)')
    parser.add_argument('--crew', required=True, help='Destination Crew ID, created normally beforehand')
    parser.add_argument('--agent-binary', type=Path, help='This refactor build, for input validation and older JSON-only chat archives')
    parser.add_argument('--leave-schedules', action='store_true', help='Archive already-disabled schedules byte-identically outside active stores; convert no timers. The receipt lists their IDs/path/hash for the later Crew timer migration.')
    parser.add_argument('--split-schedule-groups', action='store_true', help='Explicitly split a batched timer into one timer per saved input preset, preserving timing and enabled state')
    parser.add_argument('--deployment-mode', choices=('local', 'server'), default='local', help='Use the existing browser conversion policy for this installation')
    parser.add_argument('--apply', action='store_true')
    parser.add_argument('--finish-runtime', action='store_true', help='Move remaining runtime for one already converted Crew workflow')
    parser.add_argument('--cleanup-learnings', action='store_true', help='Report retired learnings; --apply backs up and deletes them only after the Crew stamps contract 1.0.52')
    args=parser.parse_args()
    try:
        if args.leave_schedules and (args.finish_runtime or args.cleanup_learnings):
            raise ValueError('--leave-schedules is only for adoption, not relocation or cleanup.')
        if args.finish_runtime and args.cleanup_learnings:
            raise ValueError('Choose either runtime relocation or post-upgrade cleanup.')
        if args.cleanup_learnings:
            conversion = LearningCleanup(args.docs,args.state,args.workflow,args.crew)
        elif args.finish_runtime:
            conversion = RuntimeRelocation(args.docs,args.state,args.workflow,args.crew)
        else:
            conversion = Conversion(args.docs,args.state,args.workflow,args.crew,args.agent_binary,split_schedule_groups=args.split_schedule_groups,deployment_mode=args.deployment_mode,leave_schedules=args.leave_schedules)
        print(json.dumps(conversion.apply() if args.apply else conversion.report(), indent=2))
    except (ValueError, SyntaxError, OSError, RuntimeError, sqlite3.Error) as exc:
        print(json.dumps({'status':'needs attention','error':str(exc)}))
        return 1
    return 0


if __name__ == '__main__':
    sys.exit(main())
