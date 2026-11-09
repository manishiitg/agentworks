"""PLAT-746-265: one-Goal conversion preserves history and cannot widen access."""
from contextlib import closing
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import sqlite3
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

spec=importlib.util.spec_from_file_location('goal_conversion',Path(__file__).with_name('migrate-goal-to-crew.py'))
m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)


def write(p,v):
    p.parent.mkdir(parents=True,exist_ok=True)
    p.write_text(json.dumps(v))


def hashes(root):
    return {str(p.relative_to(root)):hashlib.sha256(p.read_bytes()).hexdigest() for p in root.rglob('*') if p.is_file()}


class Migration(unittest.TestCase):
    def setUp(self):
        self.tmp=tempfile.TemporaryDirectory();self.addCleanup(self.tmp.cleanup)
        self.base=Path(self.tmp.name).resolve();self.docs=self.base/'docs';self.state=self.base/'state'
        self.source=self.docs/'Workflow/old-goal';self.private=self.docs/'_system/crew-data/crew'
        self.runtime=self.private/'workflows/old-goal'
        write(self.state/'ownership/workflows.json',{'Workflow/old-goal':'owner'})
        write(self.state/'ownership/projects.json',{'projects':{'work/target':{'product':'work','folder':'target','owner_id':'owner','project_id':'crew','private':True,'run_people':['runner']}}})
        # The Run person joined the destination Crew's Team (decisions log, 2026-11-08).
        write(self.state/'team-folders/crew/team.json',{'project_id':'crew','owner_id':'owner','on':True,'people':[{'user_id':'runner','invited':False}]})
        (self.docs/'Crew/target').mkdir(parents=True)
        write(self.private/'product.json',{'id':'crew','product':'work','main_chat_key':'crew:chat:existing'})
        write(self.source/'workflow.json',{'id':'old-goal','code_layout_version':1,'label':'Revenue goal','created_by':'owner','access':{'owners':['owner'],'readers':['runner']},'schedules':[], 'capabilities':{'selected_secrets':['api-name']}})
        (self.source/'soul').mkdir(parents=True)
        (self.source/'soul/soul.md').write_text('## Objective\nGrow qualified revenue.\n\n## Success Criteria\nVerified revenue.\n')
        write(self.source/'planning/plan.json',{'steps':[{'id':'one','type':'scripted'}]})
        write(self.source/'variables/variables.json',{'variables':[],'groups':[{'name':'default','values':{},'enabled':True}]})
        write(self.source/'code/one/main.py',{'source':'original'})
        # PLAT-746-336: valid bridge API names are not direct tracking-table SQL.
        (self.source/'code/one/main.py').write_text("bridge('record_goal_observations', {'observations': []})\n")
        write(self.source/'runs/iteration-1/run_metadata.json',{'run_id':'original-run'})
        write(self.source/'skills/example/SKILL.md',{'untouched':'skill'})
        write(self.source/'knowledgebase/example.json',{'untouched':'knowledge'})
        write(self.source/'db/reports/index.html',{'report':'original business report'})
        write(self.docs/'Workflow/unrelated/workflow.json',{'id':'unrelated'})
        self.key='old-goal:chat:owner-'+hashlib.sha256(b'owner').hexdigest()[:16]
        record={'profile_id':'agentworks','conversation_key':self.key,'conversation_id':'conversation-original','session_id':'session-original','workspace_path':'Workflow/old-goal','resource_id':'old-goal'}
        write(self.docs/'_users/owner/chat_history/product-conversations.json',{'entries':{'agentworks/'+self.key:record},'agents':{'agentworks/'+self.key:{'name':'Revenue Builder'}}})
        self.oldpath='Workflow/old-goal/builder/conversation/users/owner/2026-11-04/session-session-original-conversation.json'
        write(self.docs/self.oldpath,{'session_id':'session-original','user_id':'owner','workspace_path':'Workflow/old-goal','conversation_history':[{'role':'user','content':'Do not rewrite Workflow/old-goal inside messages'}]})
        write(self.docs/'_users/owner/chat_history/index.json',{'entries':{self.oldpath:{'session':{'session_id':'session-original','workspace_path':'Workflow/old-goal','conversation_path':self.oldpath},'conversation_path':self.oldpath}}})
        # Real personal history also contains delivery receipts, with owner rather
        # than transcript user_id. These are historical receipts, not chat archives.
        self.receipt=self.docs/'_users/owner/chat_history/submissions/delivery.json'
        write(self.receipt,{'owner':'owner','project':'old-goal','session_id':'session-original','state':'delivered'})
        for filename,field in [('schedules.json','schedules'),('functions.json','functions'),('webhooks.json','webhooks')]:
            write(self.source/filename,{field:[{'id':field+'-original','name':field,'enabled':False,'run_as':'owner','project':{'kind':'goal','id':'old-goal'},'action':{'type':'run','tab':self.key},'input':{'groups':['default']},'auth':{'encrypted_secret':'opaque-not-printed'}}]})
        with closing(sqlite3.connect(self.source/'db/db.sqlite')) as db, db:
            db.execute('CREATE TABLE business_data(value TEXT)');db.execute("INSERT INTO business_data VALUES ('unchanged')")
            db.execute('CREATE TABLE workflow_goal_metrics(metric_id TEXT PRIMARY KEY, definition_json TEXT NOT NULL, active INTEGER NOT NULL DEFAULT 1)')
            db.execute('INSERT INTO workflow_goal_metrics VALUES (?,?,1)',('revenue',json.dumps({'id':'revenue','role':'primary','unit':'USD','target':20})))
            db.execute('CREATE TABLE goal_observations(observation_id TEXT PRIMARY KEY,criterion_id TEXT,metric TEXT,run_id TEXT,route TEXT,environment TEXT,value REAL,status TEXT,unit TEXT,observed_at TEXT,window_start TEXT,window_end TEXT,evidence_json TEXT,recorded_at TEXT)')
            db.execute('CREATE INDEX idx_pulse_goal_observations_criterion ON goal_observations(criterion_id, observed_at)')
            db.execute('INSERT INTO goal_observations VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)',('original-observation','earned','revenue','source-run','','',12,'','USD','2026-11-01T00:00:00Z','','','["source report"]','2026-11-01T00:00:00Z'))

        # Existing canonical chat journal must remain untouched by conversion.
        with closing(sqlite3.connect(self.state/'structured-chat-events-v2.sqlite')) as db,db:
            db.execute('CREATE TABLE structured_chat_sessions(session_id TEXT,owner_id TEXT)')
            db.execute('CREATE TABLE structured_chat_sequences(session_id TEXT,last_sequence INTEGER)')
            db.execute("INSERT INTO structured_chat_sessions VALUES ('session-original','owner')")
            db.execute("INSERT INTO structured_chat_sequences VALUES ('session-original',2)")

    def conversion(self):return m.Conversion(self.docs,self.state,'old-goal','crew')

    def test_standalone_python_adoption_reuses_chat_entry_and_folder_conversion(self):
        # PLAT-746: server workflows have no Crew binding; same-ID Python
        # conversion must retain their real records, not create a trial copy.
        spec = importlib.util.spec_from_file_location('workflow_python', Path(__file__).with_name('migrate-workflow-to-python.py'))
        python = importlib.util.module_from_spec(spec); spec.loader.exec_module(python)
        projects_file = self.state/'ownership/projects.json'
        projects = m.read(projects_file)
        projects['projects']['work/linked'] = {'product':'work','owner_id':'owner','project_id':'linked','folder':'linked'}
        write(projects_file, projects)
        manifest = m.read(self.source/'workflow.json')
        manifest['capabilities']['workflow_context_paths'] = ['Crew/linked']
        write(self.source/'workflow.json', manifest)
        source = self.base/'python-source'
        write(source/'script.json', {'id':'old-goal','name':'Converted report'})
        (source/'main.py').write_text('print("new engine")\n')
        requirements = b'# explicit authored dependencies\r\nhttpx==0.28.1\r\n'
        (source/'requirements.txt').write_bytes(requirements)
        # PLAT-746-511: adoption must retain the original public link and move
        # only dashboard render source to the Crew's canonical report paths.
        registry_file = self.docs/'_system/dashboard_questions/resources.json'
        original_resource = {'id':'resource-kept','root':'Workflow/old-goal','project_id':'old-goal',
                             'kind':'workflow','dashboard':'overview','code':'abcdefghjk'}
        write(registry_file, {'resources':[original_resource]})
        write(self.source/'code/reports/managed/overview/rev/data.py', {'source':'render'})
        before = hashes(self.base)
        conversion = python.adopt_into_crew(self.docs,self.state,'old-goal','crew',source,deployment_mode='server')
        self.assertEqual(hashes(self.base),before)
        self.assertEqual(conversion.report()['counts']['python_scripts'],1)
        self.assertEqual(conversion.report()['counts']['dependencies']['authored_requirements'],
                         {'present':True, 'sha256':hashlib.sha256(requirements).hexdigest()})
        self.assertEqual(conversion.report()['carried_crew_links'], ['Crew/linked'])
        backup = self.state/'migrations/one-operator-backup'
        with self.assertRaisesRegex(ValueError,'outside the active workflow'):
            conversion.apply(backup=conversion.code/'bad-backup',plain_backup=True)
        self.assertEqual(hashes(self.base),before)
        # Codex1730: standalone adoption has extra dashboard move destinations;
        # both plain and archive backups must refuse before creating reports/.
        canonical_reports = self.docs/'Crew/target/code/reports'
        for plain in (False, True):
            with self.assertRaisesRegex(ValueError, 'outside the active workflow'):
                conversion.apply(backup=canonical_reports/'bad-backup', plain_backup=plain)
            self.assertEqual(hashes(self.base), before)
            self.assertFalse(canonical_reports.exists())
        result = conversion.apply(backup=backup,plain_backup=True)
        backup = backup/'old-goal'
        self.assertEqual(result['backup'],str(backup))
        self.assertTrue((backup/'source/db/db.sqlite').is_file())
        self.assertFalse((backup/'sha256.json').exists())
        with closing(sqlite3.connect(backup/'chat-journal/structured-chat-events-v2.sqlite')) as db:
            self.assertEqual(db.execute('SELECT last_sequence FROM structured_chat_sequences').fetchone()[0],2)
        self.assertFalse(self.source.exists())
        binding = m.read(conversion.binding_file)
        self.assertEqual((binding['id'],binding['workspace_path'],binding['kind'],binding['runtime_root']),('old-goal','Workflow/old-goal','script','workflows/old-goal'))
        project = m.read(self.state/'ownership/projects.json')['projects']['work/target']
        self.assertEqual(project['database_root'],'workflows/old-goal/db')
        definition = m.read(conversion.code/'script.json')
        self.assertEqual(definition['capabilities'],{'selected_secrets':['api-name'],'workflow_context_paths':['Crew/linked']})
        # The retired selected_secrets field grants nothing in the current server.
        self.assertEqual(definition['secrets'], [])
        self.assertEqual(m.read(self.private/'product.json')['workflow_context_paths'], ['Crew/linked'])
        self.assertEqual(definition['input_presets'],[{'name':'default','values':{}}])
        with closing(sqlite3.connect(self.runtime/'db/db.sqlite')) as db:
            self.assertEqual(db.execute('SELECT value FROM business_data').fetchall(),[('unchanged',)])
        for name in ('schedules','functions','webhooks'):
            entry = m.read(self.runtime/(name+'.json'))[name][0]
            self.assertEqual(entry['id'],name+'-original')
            self.assertFalse(entry['enabled'])
            self.assertEqual(entry['input']['input_preset'],'default')
            self.assertTrue(entry['action']['tab'].startswith('imported-'))
        registry = m.read(self.docs/'_users/owner/chat_history/product-conversations.json')
        self.assertTrue(any(item['session_id']=='session-original' and item['profile_id']=='work' for item in registry['entries'].values()))
        self.assertEqual((conversion.code/'main.py').read_text(),'print("new engine")\n')
        self.assertEqual((conversion.code/'requirements.txt').read_bytes(), requirements)
        self.assertTrue((conversion.code/'one/main.py').is_file())
        self.assertTrue((self.runtime/'runs/iteration-1/run_metadata.json').is_file())
        resource = m.read(registry_file)['resources'][0]
        self.assertEqual(resource,dict(original_resource,root='Crew/target',project_id='crew',kind='crew'))
        canonical = self.docs/'Crew/target/code/reports'
        moved = canonical/'managed/overview/rev/data.py'
        self.assertTrue(moved.is_file())
        self.assertFalse((conversion.code/'reports').exists())
        self.assertEqual(result['dashboard_source_moves'][0]['sha256'],hashlib.sha256(moved.read_bytes()).hexdigest())
        # Exercise the actual inspect/apply CLI on an already-adopted installation
        # with the old registry/source placement, including a no-change second run.
        import shutil
        old_reports = conversion.code/'reports'
        shutil.move(str(canonical),str(old_reports))
        write(registry_file,{'resources':[original_resource]})
        command=[sys.executable,str(Path(python.__file__)), '--docs',str(self.docs),'--state',str(self.state),
                 '--workflow-id','old-goal','--repair-dashboard-links']
        # Codex1730 P2: creating a backup under the move destination must
        # refuse before making that directory or nesting reports/reports.
        snapshot = hashes(self.base)
        refused = subprocess.run(command+['--apply','--backup',str(canonical/'backup')],capture_output=True,text=True)
        self.assertNotEqual(refused.returncode,0)
        self.assertEqual(hashes(self.base),snapshot)
        self.assertFalse(canonical.exists())
        inspect = subprocess.run(command,check=True,capture_output=True,text=True)
        self.assertEqual(hashes(self.base),snapshot)
        self.assertEqual(json.loads(inspect.stdout)['dashboard_retargets'][0]['before'],original_resource)
        subprocess.run(command+['--apply','--backup',str(self.base/'repair-backup')],check=True,capture_output=True,text=True)
        self.assertTrue(moved.is_file())
        self.assertEqual(m.read(registry_file)['resources'][0],resource)
        snapshot = hashes(self.base)
        again = subprocess.run(command+['--apply'],check=True,capture_output=True,text=True)
        self.assertEqual(hashes(self.base),snapshot)
        self.assertEqual(json.loads(again.stdout)['dashboard_source_moves'],[])
        self.assertEqual(json.loads(again.stdout)['dashboard_retargets'],[])
        with self.assertRaisesRegex(ValueError,'standalone workflow ID'):
            python.adopt_into_crew(self.docs,self.state,'old-goal','crew',source,deployment_mode='server')

    def test_moved_attachment_keeps_target_identity_and_refuses_other_owner_or_missing(self):
        # PLAT-746-396: a saved old root caused preflight refusals after migration.
        file = self.source/'workflow.json'
        manifest = m.read(file)
        attachment = {'id':'attachment','alias':'source','crew_profile_id':'work','crew_project_id':'crew','crew_workspace_path':'_users/owner/Chats/Work/projects/target','created_at':'original'}
        manifest['crew_attachments'] = [attachment]
        manifest['capabilities']['browser_mode'] = 'auto'
        write(file,manifest)
        before = hashes(self.base)
        candidate = self.conversion()
        converted = candidate.document(candidate.definitions/'workflow.json')
        self.assertEqual(converted['crew_attachments'], [dict(attachment,crew_workspace_path='Crew/target')])
        self.assertEqual(converted['capabilities']['browser_mode'],'extension')
        self.assertEqual(hashes(self.base),before)
        server = m.Conversion(self.docs,self.state,'old-goal','crew',deployment_mode='server')
        self.assertEqual(server.document(server.definitions/'workflow.json')['capabilities']['browser_mode'],'headless')
        projects_file = self.state/'ownership/projects.json'
        projects = m.read(projects_file)
        projects['projects']['work/foreign'] = {'product':'work','folder':'foreign','project_id':'foreign','owner_id':'other'}
        write(projects_file,projects)
        for project_id,error in [('foreign','do not own'),('missing','missing or ambiguous')]:
            manifest['crew_attachments'][0]['crew_project_id'] = project_id
            write(file,manifest)
            before = hashes(self.base)
            with self.assertRaisesRegex(ValueError,error):self.conversion()
            self.assertEqual(hashes(self.base),before)

    def test_batch_split_is_explicit_and_keeps_timer_state_and_fresh_chat(self):
        # PLAT-746-396: a batch must not silently shrink to one preset.
        file = self.source/'variables/variables.json'
        definitions = m.read(file)
        definitions['groups'].append({'name':'second','values':{},'enabled':False})
        write(file, definitions)
        file = self.source/'schedules.json'
        document = m.read(file)
        original = document['schedules'][0]
        original.update(enabled=True, when={'cron':'0 9 * * *','timezone':'UTC'}, input={'groups':['default','second']})
        original['action']['conversation'] = 'isolated'
        original['input_mapping'] = {}
        write(file, document)
        hooks=m.read(self.source/'webhooks.json')
        hooks['webhooks'][0]['action']['conversation']='isolated'
        write(self.source/'webhooks.json',hooks)
        functions=m.read(self.source/'functions.json')
        functions['functions'][0].update(execution='fresh')
        functions['functions'][0]['action']['conversation']='fresh'
        write(self.source/'functions.json',functions)
        before = hashes(self.base)
        with self.assertRaisesRegex(ValueError, 'batch needs an owner decision'):
            self.conversion()
        self.assertEqual(hashes(self.base), before)
        conversion = m.Conversion(self.docs,self.state,'old-goal','crew',split_schedule_groups=True)
        conversion.apply()
        saved = m.read(self.runtime/'schedules.json')['schedules']
        self.assertEqual([row['input']['input_preset'] for row in saved], ['default','second'])
        self.assertEqual(saved[0]['id'], original['id'])
        self.assertEqual(len({row['id'] for row in saved}), 2)
        for row in saved:
            self.assertEqual(row['when'], original['when'])
            self.assertTrue(row['enabled'])
            self.assertEqual(row['action']['conversation'], 'isolated')
            self.assertNotIn('input_mapping', row)
        self.assertEqual(m.read(self.runtime/'webhooks.json')['webhooks'][0]['action']['conversation'],'isolated')
        function=m.read(self.runtime/'functions.json')['functions'][0]
        self.assertEqual(function['action']['conversation'],'new')
        self.assertEqual(function['execution'],'new')
        after = hashes(self.base)
        self.conversion().apply()
        self.assertEqual(hashes(self.base), after)

    def test_missing_owner_needs_consistent_chat_archive_and_entry_evidence(self):
        # Never substitute a machine-wide default for a missing execution owner.
        write(self.state/'ownership/workflows.json', {})
        conversion = self.conversion()
        self.assertEqual(conversion.report()['owner_evidence']['registered_people'], ['owner'])
        file = self.source/'functions.json'
        document = m.read(file); document['functions'][0]['run_as'] = 'another-person'; write(file, document)
        before = hashes(self.base)
        with self.assertRaisesRegex(ValueError, 'evidence must identify one person'):
            self.conversion()
        self.assertEqual(hashes(self.base), before)

    def test_slot_cache_is_skipped_in_backup_and_never_read_or_deleted(self):
        # PLAT-746-435: real conversion failed on a dangling sandbox link and
        # slot-owned 0600 cache files. Mode 000 reproduces the read refusal
        # without requiring a second OS account in the fixture.
        caches = ['.cache', '.tmp', '.sandbox-cache', '.scratch-generated']
        for name in caches:
            cache = self.source/name
            cache.mkdir()
            (cache/'unreadable').write_bytes(b'generated')
            (cache/'unreadable').chmod(0)
            (cache/'dangling').symlink_to('absent-target')
        conversion = self.conversion()
        self.assertEqual(sorted(conversion.report()['backup_skipped']), sorted(caches))
        result = conversion.apply(plain_backup=True)
        backup = Path(result['backup'])
        self.assertEqual(json.loads((backup/'skipped.json').read_text()), sorted('source/'+name for name in caches))
        self.assertFalse(any((backup/'source'/name).exists() for name in caches))
        for name in caches:
            cache = self.runtime/name
            self.assertTrue((cache/'dangling').is_symlink())
            self.assertEqual((cache/'unreadable').stat().st_mode & 0o777, 0)
        self.assertTrue((self.runtime/'db/db.sqlite').is_file())
        # The optional archive backup follows exactly the same exclusion.
        archive_backup = self.base/'archive-backup'
        m.verified_backup(archive_backup, [('runtime', self.runtime)])
        self.assertEqual(json.loads((archive_backup/'skipped.json').read_text()), sorted('runtime/'+name for name in caches))
        with m.tarfile.open(archive_backup/'files.tar.gz') as archive:
            self.assertFalse(any('/'+name+'/' in p for p in archive.getnames() for name in caches))

        (self.runtime/'code').mkdir(exist_ok=True)
        bad = self.runtime/'code/broken-link'
        bad.symlink_to('absent-target')
        with self.assertRaisesRegex(ValueError, str(bad)):
            m.no_links(self.runtime)

    def test_unsupported_source_layout_is_left_unchanged(self):
        file = self.source/'workflow.json'
        original = m.read(file)
        for layout in ('omitted', 0, None, 99, True):
            with self.subTest(layout=layout):
                document = dict(original)
                if layout == 'omitted': document.pop('code_layout_version', None)
                else: document['code_layout_version'] = layout
                write(file, document)
                before = hashes(self.base)
                with self.assertRaisesRegex(ValueError, 'source layout'):
                    self.conversion()
                self.assertEqual(before, hashes(self.base))

    def test_report_does_not_create_or_update_wal_sidecars(self):
        # PLAT-746-396: the real copy dry-run created SQLite sidecars with
        # mode=ro. It must inspect committed WAL data without touching source.
        database = self.source/'db/db.sqlite'
        with closing(sqlite3.connect(database)) as db:
            db.execute('PRAGMA journal_mode=WAL')
        before = hashes(self.base)
        m.Conversion(self.docs,self.state,'old-goal','crew').report()
        self.assertEqual(hashes(self.base), before)
        with closing(sqlite3.connect(database)) as db:
            db.execute('INSERT INTO goal_observations(observation_id,metric,value) VALUES (?,?,?)', ('in-wal','revenue',5))
            db.commit()
            self.assertGreater(database.with_name(database.name+'-wal').stat().st_size, 0)
            before = hashes(self.base)
            report = m.Conversion(self.docs,self.state,'old-goal','crew').report()
            self.assertEqual(report['counts']['observations'], 2)
            self.assertEqual(hashes(self.base), before)

    def test_learning_cleanup_requires_stamp_backs_up_and_reruns_without_writes(self):
        helper = self.source/'learnings/_global/helper.py'
        helper.parent.mkdir(parents=True); helper.write_text('original helper bytes')
        conversion = m.Conversion(self.docs,self.state,'old-goal','crew')
        conversion.apply()
        cleanup = m.LearningCleanup(self.docs,self.state,'old-goal','crew')
        before = hashes(self.base)
        self.assertEqual(cleanup.apply()['status'], 'not upgraded')
        self.assertEqual(hashes(self.base), before)
        manifest = m.read(cleanup.manifest)
        manifest.update(version='1.0.52', code_layout_version=1); write(cleanup.manifest, manifest)
        result = cleanup.apply()
        with m.tarfile.open(Path(result['backup'])/'files.tar.gz') as archive:
            self.assertEqual(archive.extractfile('learnings/_global/helper.py').read(), b'original helper bytes')
        self.assertFalse(cleanup.learning.exists())
        after = hashes(self.base)
        self.assertEqual(cleanup.apply()['status'], 'already clean')
        self.assertEqual(hashes(self.base), after)
        self.assertTrue((self.runtime/'runs/iteration-1/run_metadata.json').is_file())

    def test_retired_measurements_remain_unchanged_without_creating_crew_goals(self):
        # PLAT-746-364: preserve historical data, not the retired Goal concept.
        # Existing steps still read ordinary helper files under this path.
        helper = self.source/'learnings/_global/helper.py'
        helper.parent.mkdir(parents=True)
        helper.write_text('from pathlib import Path\ndef read_note(): return Path(__file__).with_name("note.txt").read_text()\n')
        helper.with_name('note.txt').write_text('preserved guidance')
        with closing(sqlite3.connect(self.source/'db/db.sqlite')) as db, db:
            db.execute('INSERT INTO business_data VALUES (?)', (str(self.source/'db/reports/index.html'),))
        before = (self.source/'db/db.sqlite').read_bytes()
        conversion = m.Conversion(self.docs,self.state,'old-goal','crew')
        self.assertEqual(conversion.report()['database_columns_with_old_paths'], [{'database':'db/db.sqlite','table':'business_data','column':'value'}])
        self.assertEqual(conversion.report()['counts']['goals'], 0)
        conversion.apply()
        self.assertEqual((self.runtime/'db/db.sqlite').read_bytes(), before)
        self.assertFalse((self.private/'db/db.sqlite').exists())
        moved = self.runtime/'learnings/_global/helper.py'
        spec = importlib.util.spec_from_file_location('retained_helper', moved)
        module = importlib.util.module_from_spec(spec); spec.loader.exec_module(module)
        self.assertEqual(module.read_note(), 'preserved guidance')
        with closing(sqlite3.connect(self.runtime/'db/db.sqlite')) as db:
            self.assertEqual(db.execute('SELECT COUNT(*) FROM workflow_goal_metrics').fetchone()[0], 1)
            self.assertEqual(db.execute('SELECT run_id,evidence_json FROM goal_observations').fetchone(), ('source-run','["source report"]'))
            self.assertIsNotNone(db.execute("SELECT 1 FROM sqlite_master WHERE name='idx_pulse_goal_observations_criterion'").fetchone())

    def test_legacy_function_groups_convert_before_contract_validation(self):
        # PR498: runtime validation rejects groups, so validate only after the
        # one-Goal converter has produced the final preset/function envelope.
        binary = os.environ.get('AGENTWORKS_TEST_AGENT_BINARY')
        if not binary:
            self.skipTest('set AGENTWORKS_TEST_AGENT_BINARY to the isolated refactor build')
        functions = m.read(self.source/'functions.json')
        functions['functions'][0]['input_schema'] = {'type': 'object', 'properties': {'count': {'type': 'integer', 'minimum': 1}}, 'required': ['count']}
        write(self.source/'functions.json', functions)
        definitions = {'variables': [{'name': 'count', 'value': 'bad'}], 'groups': [{'name': 'default', 'values': {}}]}
        write(self.source/'variables/variables.json', definitions)
        before = hashes(self.base)
        with self.assertRaisesRegex(ValueError, 'Input conversion needs an author decision'):
            m.Conversion(self.docs, self.state, 'old-goal', 'crew', binary).report()
        self.assertEqual(hashes(self.base), before)
        definitions['variables'][0]['value'] = '2'
        write(self.source/'variables/variables.json', definitions)
        conversion = m.Conversion(self.docs, self.state, 'old-goal', 'crew', binary)
        self.assertEqual(conversion.report()['status'], 'ready')
        conversion.apply()
        fn = m.read(self.runtime/'functions.json')['functions'][0]
        self.assertEqual(fn['inputs'], ['count'])
        self.assertEqual(fn['input'], {'input_preset': 'default'})
        self.assertNotIn('input_schema', fn)

    def test_one_goal_preserves_history_ids_other_goal_and_private_chats(self):
        # PLAT-746: the first real migrated timer lost its canonical JSONL
        # companion, then failed creating a new one in root-created parents.
        history = (self.docs/self.oldpath).with_suffix('.history.jsonl')
        original_history = b'{"role":"user","content":"Preserve the entire append-only history"}\n'
        history.write_bytes(original_history)
        history.chmod(0o640)
        original_stat = history.stat()
        # Refuse an existing destination rather than merge or overwrite history.
        collision = self.private/'builder/conversation/users/owner'/history.parent.name/history.name
        collision.parent.mkdir(parents=True, exist_ok=True)
        collision.write_bytes(b'existing destination history\n')
        before_collision = hashes(self.base)
        with self.assertRaisesRegex(ValueError, 'Destination already exists'):
            self.conversion()
        self.assertEqual(hashes(self.base), before_collision)
        collision.unlink()
        registry_path = self.docs/'_users/owner/chat_history/product-conversations.json'
        registry = m.read(registry_path)
        registry['entries']['work/crew:chat:existing'] = {'profile_id':'work','conversation_key':'crew:chat:existing','session_id':'destination-session','resource_id':'crew','workspace_path':'Crew/target'}
        # A second imported chat is independent; only the owner's primary Builder becomes Main.
        extra = dict(registry['entries']['agentworks/'+self.key], conversation_key='old-goal:chat:research', session_id='research-session')
        registry['entries']['agentworks/old-goal:chat:research'] = extra
        write(registry_path, registry)

        # History indexes use archive paths, never canonical Builder tab keys.
        # Two rows must not trigger primary-chat selection in this second pass.
        index_path = self.docs/'_users/owner/chat_history/index.json'
        index = m.read(index_path)
        first = next(iter(index['entries'].values()))
        first['workspace_path'] = 'Workflow/old-goal'
        index['entries']['other-archive.json'] = {'workspace_path':'Workflow/old-goal', 'session':{'session_id':'research-session','workspace_path':'Workflow/old-goal'}}
        write(index_path, index)

        before=hashes(self.base);c=self.conversion()
        self.assertEqual(c.report()['status'],'ready');self.assertEqual(hashes(self.base),before)
        result=c.apply();self.assertTrue(Path(result['backup'],'files.tar.gz').exists())
        self.assertFalse((self.source/'workflow.json').exists())
        self.assertTrue((self.docs/'Crew/target/.agentworks/workflows/old-goal/workflow.json').exists())
        self.assertEqual((self.runtime/'runs/iteration-1/run_metadata.json').read_text(),'{"run_id": "original-run"}')
        after=hashes(self.base)
        for name in ['docs/Workflow/unrelated/workflow.json',str(self.receipt.relative_to(self.base))]:
            self.assertEqual(before[name],after[name])
        for name in ['skills/example/SKILL.md','knowledgebase/example.json','db/reports/index.html']:
            self.assertEqual(before['docs/Workflow/old-goal/'+name],after[str((self.runtime/name).relative_to(self.base))])
        self.assertFalse(self.source.exists())
        self.assertFalse((self.private/'db/db.sqlite').exists())
        with closing(sqlite3.connect(self.runtime/'db/db.sqlite')) as db:
            self.assertEqual(db.execute('SELECT * FROM business_data').fetchall(), [('unchanged',)])
            self.assertEqual(db.execute('SELECT observation_id,criterion_id,metric,run_id FROM goal_observations').fetchone(), ('original-observation','earned','revenue','source-run'))
        for filename,field in [('schedules.json','schedules'),('functions.json','functions'),('webhooks.json','webhooks')]:
            entry=m.read(self.runtime/filename)[field][0]
            self.assertFalse(entry['enabled']);self.assertTrue(entry['action']['tab'].startswith('imported-'))
            self.assertEqual(entry['input'],{'input_preset':'default'})
            self.assertEqual(entry['auth']['encrypted_secret'],'opaque-not-printed')
        transcript=next((self.private/'builder/conversation').rglob('*-conversation.json'))
        self.assertEqual(m.read(transcript)['conversation_history'][0]['content'],'Do not rewrite Workflow/old-goal inside messages')
        self.assertEqual(m.read(transcript)['user_id'],'owner')
        moved_history = transcript.with_suffix('.history.jsonl')
        self.assertEqual(moved_history.read_bytes(), original_history)
        self.assertEqual(moved_history.stat().st_mode & 0o777, 0o640)
        self.assertEqual((moved_history.stat().st_uid, moved_history.stat().st_gid),
                         (original_stat.st_uid, original_stat.st_gid))
        self.assertFalse((self.runtime/history.relative_to(self.source)).exists())

        registry=m.read(self.docs/'_users/owner/chat_history/product-conversations.json')
        main_key = m.read(self.private/'product.json')['main_chat_key']
        row = registry['entries']['work/'+main_key]
        self.assertEqual(registry['agents']['work/'+main_key]['name'], 'Main')
        self.assertEqual([r['session_id'] for r in registry['previous']['work/'+main_key]], ['destination-session'])
        self.assertEqual(len(registry['entries']), 2)
        self.assertTrue(any(r['session_id'] == 'research-session' for r in registry['entries'].values()))
        self.assertEqual(row['session_id'],'session-original');self.assertEqual(row['resource_id'],'crew')
        # PLAT-746-396: own workflows use the Crew binding, not a stale attachment.
        for entry in registry['entries'].values():
            self.assertNotIn('Workflow/old-goal', entry.get('workflow_context_paths', []))
        for entries in registry.get('previous', {}).values():
            for entry in entries:
                self.assertNotIn('Workflow/old-goal', entry.get('workflow_context_paths', []))
        index=m.read(self.docs/'_users/owner/chat_history/index.json')
        item=next(row for row in index['entries'].values() if row['session']['session_id']=='session-original')
        self.assertTrue(item['session']['conversation_path'].startswith('Crew/target/'))
        self.assertEqual(self.conversion().report()['status'],'already migrated')
        self.conversion().apply();self.assertEqual(after,hashes(self.base))

    def test_unregistered_owner_history_keeps_bytes_without_creating_tabs(self):
        # PLAT-746-364: real older archives have no surviving registry entry.
        path = Path('builder/conversation/users/owner/2026-06-02/session-old-conversation.json')
        archive = {'session_id':'old-session','user_id':'owner',
                   'conversation_history':[{'role':'user','content':'Keep the old conversation.'}]}
        write(self.source/path, archive)
        original = (self.source/path).read_bytes()
        before = hashes(self.base)
        conversion = self.conversion()
        self.assertEqual(conversion.report()['counts']['unregistered_chat_archives'], 1)
        self.assertEqual(hashes(self.base), before)
        conversion.apply()
        self.assertEqual((self.runtime/path).read_bytes(), original)
        registry = m.read(self.docs/'_users/owner/chat_history/product-conversations.json')
        self.assertEqual(len(registry['entries']), 1)
        self.assertNotIn('old-session', str(registry))

    def test_unregistered_other_person_history_cannot_move_into_owner_records(self):
        # An archive's age never grants access to another person's private chat.
        write(self.source/'builder/conversation/users/other/2026-06-02/session-old-conversation.json',
              {'session_id':'other-session','user_id':'other','conversation_history':[]})
        before = hashes(self.base)
        with self.assertRaisesRegex(ValueError, 'private owner mapping'):
            self.conversion()
        self.assertEqual(hashes(self.base), before)

    def test_runtime_copy_failure_keeps_source_and_removes_partial_destination(self):
        # PLAT-746-435 / PR1588: copy or second-cache-rename failure leaves
        # every original file/cache intact and permits a plain retry.
        self.conversion().apply()
        self.runtime.rename(self.source)
        for name in ('.cache', '.sandbox-cache'):
            cache = self.source/name
            cache.mkdir()
            (cache/'installed-package').write_bytes(name.encode())
        source_before = hashes(self.source)
        real_copy, real_rename = m.copy_migration_tree, Path.rename

        def interrupted_copy(source, target, skipped):
            if target != self.runtime:
                return real_copy(source, target, skipped)
            target.mkdir(parents=True)
            (target/'partial').write_text('incomplete')
            raise OSError('injected runtime copy failure')

        for failure in ('copy', 'second cache rename'):
            with self.subTest(failure=failure):
                moves = []

                def interrupted_rename(path, target):
                    if path.parent == self.source and path.name in ('.cache', '.sandbox-cache'):
                        moves.append(path)
                        if len(moves) == 2:
                            raise OSError('injected second cache rename failure')
                    return real_rename(path, target)

                conversion = m.RuntimeRelocation(self.docs, self.state, 'old-goal', 'crew')
                patch = (mock.patch.object(m, 'copy_migration_tree', side_effect=interrupted_copy)
                         if failure == 'copy' else mock.patch.object(Path, 'rename', interrupted_rename))
                with patch:
                    with self.assertRaisesRegex(OSError, 'injected'):
                        conversion.apply()
                self.assertEqual(hashes(self.source), source_before)
                self.assertFalse(self.runtime.exists())
                self.assertFalse(conversion.receipt.exists())
                self.assertEqual(m.RuntimeRelocation(self.docs, self.state, 'old-goal', 'crew').report()['status'], 'ready')

    def test_finish_runtime_preserves_authored_files_and_references_without_recreating_old_folder(self):
        # PLAT-746-360: a prior source-only migration left native helpers
        # outside the runtime grant list. Finish only that workflow's records.
        self.conversion().apply()
        self.runtime.rename(self.source)
        # PLAT-746-435 / PR1520: runtime relocation must preserve installed
        # caches without descending into a dangling link inside them.
        cache = self.source/'.sandbox-cache'
        cache.mkdir()
        (cache/'dangling').symlink_to('missing-package')
        (cache/'installed-package').write_bytes(b'keep installed runtime')
        cache_inode = cache.stat().st_ino
        write(self.source/'learnings/retired.json', {'keep_in_backup':True})
        write(self.source/'runs/history/run.json', {'output_path':str(self.source/'runs/history/out.json'), 'command':'cat '+str(self.source/'old.txt')})
        with closing(sqlite3.connect(self.source/'db/db.sqlite')) as db, db:
            db.execute('CREATE TABLE file_index(id TEXT PRIMARY KEY,path TEXT,metadata TEXT) WITHOUT ROWID')
            db.execute('INSERT INTO file_index VALUES (?,?,?)',('stable-id',str(self.source/'db/assets/file.txt'),json.dumps({'path':str(self.source/'runs/history/out.json')})))
            db.execute('CREATE TABLE historical_result(id INTEGER PRIMARY KEY,result TEXT,path TEXT)')
            db.execute("INSERT INTO historical_result VALUES (1,CAST(x'FF' AS TEXT),?)",(str(self.source/'runs/history/out.json'),))
        # A stopped/crashed SQLite writer can leave committed rows in WAL.
        # Copy every sidecar and prove those rows survive opening the new DB.
        cost_db=self.source/'costs/costs.sqlite'
        cost_db.parent.mkdir()
        subprocess.run([sys.executable,'-c',"import sqlite3,os,sys; db=sqlite3.connect(sys.argv[1]); db.execute('PRAGMA journal_mode=WAL'); db.execute('CREATE TABLE cost_events(id INTEGER PRIMARY KEY,value REAL)'); db.execute('INSERT INTO cost_events VALUES (1,1.25)'); db.commit(); os._exit(0)",str(cost_db)],check=True)
        self.assertTrue(Path(str(cost_db)+'-wal').exists())
        database_before={name:value for name,value in hashes(self.source).items() if '.sqlite' in name}
        authored=hashes(self.docs/'Crew/target')
        other=hashes(self.docs/'Workflow/unrelated')
        conversion=m.RuntimeRelocation(self.docs,self.state,'old-goal','crew')
        self.assertEqual(conversion.report()['status'],'ready')
        result=conversion.apply()
        self.assertFalse(self.source.exists())
        moved_cache = self.runtime/'.sandbox-cache'
        self.assertEqual(moved_cache.stat().st_ino, cache_inode)
        self.assertTrue((moved_cache/'dangling').is_symlink())
        self.assertEqual((moved_cache/'installed-package').read_bytes(), b'keep installed runtime')
        self.assertEqual(database_before,{name:value for name,value in hashes(self.runtime).items() if '.sqlite' in name})
        self.assertEqual(result['database_columns_with_old_paths'], [
            {'database':'db/db.sqlite','table':'file_index','column':'path'},
            {'database':'db/db.sqlite','table':'file_index','column':'metadata'},
            {'database':'db/db.sqlite','table':'historical_result','column':'path'}])
        self.assertEqual(m.read(self.runtime/'learnings/retired.json'), {'keep_in_backup':True})
        self.assertEqual(authored,hashes(self.docs/'Crew/target'))
        self.assertEqual(other,hashes(self.docs/'Workflow/unrelated'))
        evidence=m.read(self.runtime/'runs/history/run.json')
        self.assertEqual(evidence['output_path'],str(self.runtime/'runs/history/out.json'))
        self.assertEqual(evidence['command'],'cat '+str(self.source/'old.txt'))
        with closing(sqlite3.connect(self.runtime/'db/db.sqlite')) as db:
            key,path,metadata=db.execute('SELECT * FROM file_index').fetchone()
            self.assertEqual(key,'stable-id')
            self.assertEqual(path,str(self.source/'db/assets/file.txt'))
            self.assertEqual(json.loads(metadata)['path'],str(self.source/'runs/history/out.json'))
            self.assertEqual(db.execute('SELECT hex(result),path FROM historical_result').fetchone(),('FF',str(self.source/'runs/history/out.json')))
        with closing(sqlite3.connect(self.runtime/'costs/costs.sqlite')) as db:
            self.assertEqual(db.execute('SELECT * FROM cost_events').fetchall(),[(1,1.25)])
        backup=Path(result['backup'])
        self.assertIn('source/learnings/retired.json',m.read(backup/'sha256.json'))
        self.assertIn('source/costs/costs.sqlite-wal',m.read(backup/'sha256.json'))
        self.assertEqual(m.RuntimeRelocation(self.docs,self.state,'old-goal','crew').apply()['status'],'already migrated')
        self.assertFalse(self.source.exists())

    def test_mechanical_move_preserves_task_text_and_code_for_crew_upgrade(self):
        # PLAT-746-396: semantic changes belong to the owning Crew upgrade,
        # not a regex which treats shell text and embedded Python alike.
        write(self.source/'planning/plan.json', {'steps':[
            {'id':'one','type':'scripted','description':'Run `python3 code/one/main.py`. See https://example.com/code/one/main.py.', 'items':[{'message':'Read `code/one/main.py`.'}]},
            {'id':'two','type':'agent','description':"Use Path('code/one/main.py').read_text()"}]})
        write(self.source/'planning/step_config.json', {'one':{'command':'python3 code/one/main.py'}})
        before_script = (self.source/'code/one/main.py').read_bytes()
        conversion = self.conversion()
        conversion.apply()
        target = self.docs/'Crew/target/.agentworks'
        plan = m.read(target/'workflows/old-goal/planning/plan.json')
        self.assertEqual(plan['steps'][0]['description'], 'Run `python3 code/one/main.py`. See https://example.com/code/one/main.py.')
        self.assertEqual(plan['steps'][0]['items'][0]['message'], 'Read `code/one/main.py`.')
        self.assertEqual(plan['steps'][1]['description'], "Use Path('code/one/main.py').read_text()")
        self.assertEqual(m.read(target/'workflows/old-goal/planning/step_config.json')['one']['command'], 'python3 code/one/main.py')
        self.assertEqual((target/'code/old-goal/one/main.py').read_bytes(), before_script)
        self.assertEqual(m.read(self.runtime/'runs/iteration-1/run_metadata.json'), {'run_id':'original-run'})

    def test_refuses_access_expansion_collision_and_escaping_source_without_writes(self):
        # Everyone with access today must be an accepted Team member; the refusal names who is missing.
        # An invitation alone grants nothing, and neither does a switched-off Team.
        team_file=self.state/'team-folders/crew/team.json'
        for on,invited in [(True,True),(False,False)]:
            write(team_file,{'project_id':'crew','owner_id':'owner','on':on,'people':[{'user_id':'runner','invited':invited}]})
            before=hashes(self.base)
            with self.assertRaisesRegex(ValueError,'accept first: runner'):self.conversion()
            self.assertEqual(before,hashes(self.base))
        write(team_file,{'project_id':'crew','owner_id':'owner','on':True,'people':[{'user_id':'runner','invited':False}]})
        self.assertEqual(self.conversion().report()['status'],'ready')
        manifest=m.read(self.source/'workflow.json');original=dict(manifest)
        manifest['access']=dict(manifest['access'],run_everyone=True);write(self.source/'workflow.json',manifest)
        with self.assertRaisesRegex(ValueError,'shared with everyone'):self.conversion()
        write(self.source/'workflow.json',original)
        private_chat=self.docs/'_users/owner/chat_history/2026-11-04/session-original-conversation.json'
        write(private_chat,{'session_id':'session-original','user_id':'another-person'})
        before=hashes(self.base)
        with self.assertRaisesRegex(ValueError,'conflicting ownership'):self.conversion()
        self.assertEqual(before,hashes(self.base));private_chat.unlink()
        destination=self.docs/'Crew/target/.agentworks/workflows/old-goal';destination.mkdir(parents=True)
        with self.assertRaisesRegex(ValueError,'already has source'):self.conversion()
        destination.rmdir()
        (self.source/'code/escape').symlink_to(self.base)
        with self.assertRaisesRegex(ValueError,'symlink'):self.conversion()


    def test_second_workflow_refused_without_changing_first(self):
        first=self.conversion().apply()
        source=self.docs/'Workflow/second-folder'
        write(source/'workflow.json',{'id':'second-id','label':'Reporting','code_layout_version':1,'access':{'owners':['owner'],'readers':['runner']}})
        owners=m.read(self.state/'ownership/workflows.json');owners['Workflow/second-folder']='owner';write(self.state/'ownership/workflows.json',owners)
        before = hashes(self.base)
        with self.assertRaisesRegex(ValueError, 'already has a workflow'):
            m.Conversion(self.docs,self.state,'second-folder','crew').apply()
        self.assertEqual(hashes(self.base), before)

    def test_ambiguous_legacy_batch_reports_before_any_writes(self):
        file=self.source/'schedules.json';doc=m.read(file);doc['schedules'][0]['input']={'groups':['one','two']};write(file,doc)
        before=hashes(self.base)
        with self.assertRaisesRegex(ValueError,'Saved entries need a decision'):self.conversion()
        self.assertEqual(before,hashes(self.base))
        doc['schedules'][0]['input']={};write(file,doc)

if __name__=='__main__':unittest.main()
